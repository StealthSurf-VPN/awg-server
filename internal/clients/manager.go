package clients

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

const maxRegenerationAttempts = 8

var ErrClientExists = errors.New("client already exists")
var ErrClientNotFound = errors.New("client not found")
var ErrEmptyClientUpdate = errors.New("at least one of protocol_version, awg_params, or routing is required")
var ErrGeneratedParamsUnchanged = errors.New("failed to generate distinct awg params")
var ErrEmptyClientIDs = errors.New("client_ids is required")
var ErrEmptyLANGroupID = errors.New("lan_group_id is required")
var ErrDuplicateClientID = errors.New("client_ids must be unique")

type ClientUpdate struct {
	ProtocolVersion    awg.ProtocolVersion
	ProtocolVersionSet bool
	AWGParams          *awg.AWGParams
	AWGParamsSet       bool
	Routing            *Routing
	RoutingSet         bool
}

type ManagerDefaults struct {
	LegacyParams       awg.AWGParams
	AWG31Params        awg.AWGParams
	DefaultVersion     awg.ProtocolVersion
	DefaultHeaderKeyID string
}

type MigrationGuard func(func() error) error

type devicePool interface {
	AddPeer(awg.Profile, int, [32]byte, *[32]byte, string) error
	RemovePeer(awg.Profile, [32]byte, string) error
	MigratePeer(awg.Profile, awg.Profile, int, [32]byte, *[32]byte, string) error
	PortForProfile(awg.Profile) (int, error)
	PublicKey() [32]byte
	ApplyLANIsolation([]awg.LANPeer) error
}

type restoreEntry struct {
	client        ClientData
	profile       awg.Profile
	publicKey     [32]byte
	presharedKey  *[32]byte
	requestedPort int
}

type RestorePlan struct {
	data               *StorageData
	defaults           ManagerDefaults
	entries            []restoreEntry
	needsNormalization bool
}

type Manager struct {
	mu       sync.RWMutex
	pool     devicePool
	storage  *Storage
	config   *config.Config
	defaults ManagerDefaults
	clients  map[string]*ClientData
	usedIPs  map[string]bool
	data     *StorageData
}

func PrepareRestorePlan(cfg *config.Config, defaults ManagerDefaults, data *StorageData) (*RestorePlan, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if data == nil {
		return nil, errors.New("storage data is required")
	}

	prospective := cloneStorageData(data)
	needsNormalization := prospective.needsNormalization
	hasAWG31Client := false

	for index := range prospective.Clients {
		version := prospective.Clients[index].ProtocolVersion
		switch version {
		case "", awg.ProtocolVersion2:
		case awg.ProtocolVersion31:
			hasAWG31Client = true
		default:
			return nil, fmt.Errorf("validate persisted protocol version: must be 2.0 or 3.1")
		}
	}

	if prospective.AWG31 == nil {
		if hasAWG31Client {
			return nil, errors.New("persisted AWG 3.1 client requires AWG 3.1 storage state")
		}

		generated, err := awg.GenerateParamsV31()
		if err != nil {
			return nil, fmt.Errorf("generate pending AWG 3.1 params: %w", err)
		}
		key, err := awg.GenerateHeaderProtectionKey()
		if err != nil {
			return nil, fmt.Errorf("generate pending AWG 3.1 header key: %w", err)
		}
		keyID, err := generateHeaderKeyID()
		if err != nil {
			return nil, err
		}

		prospective.AWG31 = &AWG31Storage{
			DefaultHeaderKeyID: keyID,
			GeneratedParams:    generated,
			HeaderKeys: map[string]HeaderKeyData{
				keyID: {HeaderProtectionKey: awg.HeaderProtectionKeyToBase64(key)},
			},
		}
		needsNormalization = true
	}

	resolvedDefaults, err := resolveManagerDefaults(defaults, prospective)
	if err != nil {
		return nil, err
	}

	entries := make([]restoreEntry, 0, len(prospective.Clients))
	seenIDs := make(map[string]struct{}, len(prospective.Clients))
	seenAddresses := make(map[string]struct{}, len(prospective.Clients))
	seenPublicKeys := make(map[[32]byte]struct{}, len(prospective.Clients))

	for index := range prospective.Clients {
		client := &prospective.Clients[index]
		if err := validateClientID(client.ID); err != nil {
			return nil, fmt.Errorf("restore client: %w", err)
		}
		if _, exists := seenIDs[client.ID]; exists {
			return nil, fmt.Errorf("restore client %q: duplicate client ID", client.ID)
		}
		seenIDs[client.ID] = struct{}{}

		if client.ProtocolVersion == "" {
			client.ProtocolVersion = awg.ProtocolVersion2
			needsNormalization = true
		}

		if client.LANGroupID == "" {
			client.LANGroupID = defaultLANGroupID(client.ID)
			needsNormalization = true
		}

		address, changed, err := normalizeStoredAddress(cfg, client.Address)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: validate address: %w", client.ID, err)
		}
		if _, exists := seenAddresses[address]; exists {
			return nil, fmt.Errorf("restore client %q: duplicate address", client.ID)
		}
		seenAddresses[address] = struct{}{}
		client.Address = address
		if changed {
			needsNormalization = true
		}

		privateKey, err := awg.Base64ToKey(client.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: decode private key: %w", client.ID, err)
		}
		publicKey, err := awg.Base64ToKey(client.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: decode public key: %w", client.ID, err)
		}
		if awg.PublicKeyFromPrivate(privateKey) != publicKey {
			return nil, fmt.Errorf("restore client %q: private and public keys do not match", client.ID)
		}
		if _, exists := seenPublicKeys[publicKey]; exists {
			return nil, fmt.Errorf("restore client %q: duplicate peer key", client.ID)
		}
		seenPublicKeys[publicKey] = struct{}{}
		presharedKey, err := decodePresharedKey(client.PresharedKey)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: %w", client.ID, err)
		}

		normalizedParams, err := awg.NormalizeOverridesForVersion(client.ProtocolVersion, client.AWGParams)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: validate awg params: %w", client.ID, err)
		}
		paramsChanged, err := persistedValueChanged(client.AWGParams, normalizedParams)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: compare normalized awg params: %w", client.ID, err)
		}
		client.AWGParams = normalizedParams
		if paramsChanged {
			needsNormalization = true
		}

		normalizedRouting, err := NormalizeRouting(client.Routing)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: validate routing: %w", client.ID, err)
		}
		routingChanged, err := persistedValueChanged(client.Routing, normalizedRouting)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: compare normalized routing: %w", client.ID, err)
		}
		client.Routing = normalizedRouting
		if routingChanged {
			needsNormalization = true
		}

		if client.ProtocolVersion == awg.ProtocolVersion2 && client.headerKeyID != "" {
			return nil, fmt.Errorf("restore client %q: legacy profile must not reference a header protection key", client.ID)
		}

		profile, err := effectiveProfileForData(resolvedDefaults, prospective, client.ProtocolVersion, client.AWGParams, client.headerKeyID)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: validate profile: %w", client.ID, err)
		}

		entries = append(entries, restoreEntry{
			client:        cloneClientData(*client),
			profile:       profile,
			publicKey:     publicKey,
			presharedKey:  presharedKey,
			requestedPort: profile.Params().Port,
		})
	}

	if err := validateRestoreInterfaces(cfg, entries); err != nil {
		return nil, err
	}

	return &RestorePlan{
		data:               prospective,
		defaults:           resolvedDefaults,
		entries:            entries,
		needsNormalization: needsNormalization,
	}, nil
}

func NewManagerFromRestorePlan(pool devicePool, storage *Storage, cfg *config.Config, plan *RestorePlan) (*Manager, error) {
	if pool == nil {
		return nil, errors.New("device pool is required")
	}
	if storage == nil {
		return nil, errors.New("storage is required")
	}
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if plan == nil {
		return nil, errors.New("restore plan is required")
	}

	data := cloneStorageData(plan.data)
	manager := &Manager{
		pool:     pool,
		storage:  storage,
		config:   cfg,
		defaults: cloneManagerDefaults(plan.defaults),
		clients:  make(map[string]*ClientData, len(plan.entries)),
		usedIPs:  make(map[string]bool, len(plan.entries)),
		data:     data,
	}

	for _, entry := range plan.entries {
		if err := pool.AddPeer(entry.profile, entry.requestedPort, entry.publicKey, entry.presharedKey, entry.client.Address); err != nil {
			return nil, fmt.Errorf("restore client %q: add peer: %w", entry.client.ID, err)
		}
	}

	if err := pool.ApplyLANIsolation(lanPeers(data.Clients)); err != nil {
		return nil, fmt.Errorf("restore LAN firewall: %w", err)
	}

	if plan.needsNormalization {
		if err := storage.Save(data); err != nil {
			return nil, fmt.Errorf("persist storage normalization: %w", err)
		}
		data.needsNormalization = false
	}

	for _, client := range data.Clients {
		copy := cloneClientData(client)
		manager.clients[copy.ID] = &copy
		manager.usedIPs[copy.Address] = true
		log.Printf("restored client %s (%s)", copy.ID, copy.Address)
	}

	log.Printf("loaded %d clients from storage", len(manager.clients))

	return manager, nil
}

func (m *Manager) prospectiveData() *StorageData {
	return cloneStorageData(m.data)
}

type persistenceError struct {
	operation   string
	saveErr     error
	rollbackErr error
}

func (e *persistenceError) Error() string {
	message := e.operation + ": " + e.saveErr.Error()
	if e.rollbackErr != nil {
		message += "; rollback device state: " + e.rollbackErr.Error()
	}

	return message
}

func (e *persistenceError) Unwrap() error {
	return e.saveErr
}

func persistenceFailure(operation string, saveErr, rollbackErr error) error {
	return &persistenceError{
		operation:   operation,
		saveErr:     saveErr,
		rollbackErr: rollbackErr,
	}
}

func (m *Manager) CreateClient(name string, params *awg.AWGParams, routing *Routing, lanGroupID string) (*ClientData, error) {
	return m.CreateClientWithVersion(name, m.defaults.DefaultVersion, params, routing, lanGroupID)
}

func (m *Manager) CreateClientWithVersion(name string, version awg.ProtocolVersion, params *awg.AWGParams, routing *Routing, lanGroupID string) (*ClientData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateClientID(name); err != nil {
		return nil, err
	}
	if !version.Valid() {
		return nil, fmt.Errorf("%w: protocol_version must be 2.0 or 3.1", awg.ErrInvalidParams)
	}
	if _, exists := m.clients[name]; exists {
		return nil, ErrClientExists
	}

	normalizedParams, err := awg.NormalizeOverridesForVersion(version, params)
	if err != nil {
		return nil, err
	}
	normalizedRouting, err := NormalizeRouting(routing)
	if err != nil {
		return nil, err
	}
	if lanGroupID == "" {
		lanGroupID = defaultLANGroupID(name)
	}

	prospective := m.prospectiveData()
	headerKeyID := ""
	if version == awg.ProtocolVersion31 {
		headerKeyID = m.defaults.DefaultHeaderKeyID
	}

	profile, err := effectiveProfileForData(m.defaults, prospective, version, normalizedParams, headerKeyID)
	if err != nil {
		return nil, err
	}

	privateKey, err := awg.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key pair: %w", err)
	}
	publicKey := awg.PublicKeyFromPrivate(privateKey)
	presharedKey, err := awg.GeneratePresharedKey()
	if err != nil {
		return nil, fmt.Errorf("generate preshared key: %w", err)
	}
	ip, err := m.allocateIP()
	if err != nil {
		return nil, fmt.Errorf("allocate IP: %w", err)
	}

	client := ClientData{
		ID:              name,
		ProtocolVersion: version,
		PrivateKey:      awg.KeyToBase64(privateKey),
		PublicKey:       awg.KeyToBase64(publicKey),
		PresharedKey:    awg.KeyToBase64(presharedKey),
		Address:         ip,
		LANGroupID:      lanGroupID,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		AWGParams:       normalizedParams,
		Routing:         normalizedRouting,
		headerKeyID:     headerKeyID,
	}
	prospective.Clients = append(prospective.Clients, cloneClientData(client))
	markAndSweepHeaderKeys(prospective)

	if err := m.blockLANLocked(); err != nil {
		return nil, err
	}
	if err := m.pool.AddPeer(profile, profile.Params().Port, publicKey, &presharedKey, ip); err != nil {
		return nil, fmt.Errorf("add peer to device: %w", err)
	}
	if err := m.storage.Save(prospective); err != nil {
		rollbackErr := m.pool.RemovePeer(profile, publicKey, ip)

		return nil, persistenceFailure("save created client", err, rollbackErr)
	}

	m.commitClientLocked(client, prospective)
	if err := m.rebuildLANLocked(); err != nil {
		return nil, err
	}

	result := cloneClientData(client)

	return &result, nil
}

func (m *Manager) UpdateClient(id string, update ClientUpdate, migrationGuard MigrationGuard) (*ClientData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[id]
	if !ok {
		return nil, ErrClientNotFound
	}

	targetVersion, params, routing, resetParams, err := resolveClientUpdate(client, update)
	if err != nil {
		return nil, err
	}

	return m.applyClientUpdateLocked(client, targetVersion, params, routing, resetParams, "", nil, migrationGuard)
}

func (m *Manager) RegenerateAWGParams(id string, migrationGuard MigrationGuard) (*ClientData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[id]
	if !ok {
		return nil, ErrClientNotFound
	}
	if migrationGuard == nil {
		return nil, errors.New("migration guard is required")
	}
	if !client.ProtocolVersion.Valid() {
		return nil, fmt.Errorf("%w: protocol_version must be 2.0 or 3.1", awg.ErrInvalidParams)
	}

	oldProfile, err := effectiveProfileForData(m.defaults, m.data, client.ProtocolVersion, client.AWGParams, client.headerKeyID)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < maxRegenerationAttempts; attempt++ {
		switch client.ProtocolVersion {
		case awg.ProtocolVersion2:
			generated, err := awg.GenerateParams()
			if err != nil {
				return nil, fmt.Errorf("generate awg params: %w", err)
			}

			params, err := awg.NormalizeOverridesForVersion(awg.ProtocolVersion2, awg.ApplyGeneratedParams(client.AWGParams, *generated))
			if err != nil {
				return nil, err
			}
			profile, err := effectiveProfileForData(m.defaults, m.data, awg.ProtocolVersion2, params, "")
			if err != nil {
				return nil, err
			}
			if profile.Key() == oldProfile.Key() {
				continue
			}

			return m.applyClientUpdateLocked(client, awg.ProtocolVersion2, params, cloneRouting(client.Routing), false, "", nil, migrationGuard)
		case awg.ProtocolVersion31:
			generated, err := awg.GenerateParamsV31()
			if err != nil {
				return nil, fmt.Errorf("generate AWG 3.1 params: %w", err)
			}
			params, err := awg.NormalizeOverridesForVersion(awg.ProtocolVersion31, awg.ApplyGeneratedParamsV31(client.AWGParams, *generated))
			if err != nil {
				return nil, err
			}
			keyID, keyData, err := m.newHeaderKeyData()
			if err != nil {
				return nil, err
			}

			return m.applyClientUpdateLocked(client, awg.ProtocolVersion31, params, cloneRouting(client.Routing), false, keyID, &keyData, migrationGuard)
		}
	}

	return nil, ErrGeneratedParamsUnchanged
}

func resolveClientUpdate(client *ClientData, update ClientUpdate) (awg.ProtocolVersion, *awg.AWGParams, *Routing, bool, error) {
	if !update.ProtocolVersionSet && !update.AWGParamsSet && !update.RoutingSet {
		return "", nil, nil, false, ErrEmptyClientUpdate
	}

	targetVersion := client.ProtocolVersion
	if update.ProtocolVersionSet {
		targetVersion = update.ProtocolVersion
	}
	if !targetVersion.Valid() {
		return "", nil, nil, false, fmt.Errorf("%w: protocol_version must be 2.0 or 3.1", awg.ErrInvalidParams)
	}

	params := cloneStoredAWGParams(client.AWGParams)
	resetParams := update.AWGParamsSet && update.AWGParams == nil
	if update.AWGParamsSet {
		params = update.AWGParams
	}
	normalizedParams, err := awg.NormalizeOverridesForVersion(targetVersion, params)
	if err != nil {
		return "", nil, nil, false, err
	}

	routing := cloneRouting(client.Routing)
	if update.RoutingSet {
		routing = update.Routing
	}
	normalizedRouting, err := NormalizeRouting(routing)
	if err != nil {
		return "", nil, nil, false, err
	}

	return targetVersion, normalizedParams, normalizedRouting, resetParams, nil
}

func (m *Manager) applyClientUpdateLocked(client *ClientData, targetVersion awg.ProtocolVersion, params *awg.AWGParams, routing *Routing, resetParams bool, replacementHeaderKeyID string, replacementHeaderKey *HeaderKeyData, migrationGuard MigrationGuard) (*ClientData, error) {
	oldProfile, err := effectiveProfileForData(m.defaults, m.data, client.ProtocolVersion, client.AWGParams, client.headerKeyID)
	if err != nil {
		return nil, err
	}

	nextClient := cloneClientData(*client)
	nextClient.ProtocolVersion = targetVersion
	nextClient.AWGParams = cloneStoredAWGParams(params)
	nextClient.Routing = cloneRouting(routing)
	nextClient.headerKeyID = client.headerKeyID

	if targetVersion != client.ProtocolVersion {
		if targetVersion == awg.ProtocolVersion31 {
			nextClient.headerKeyID = m.defaults.DefaultHeaderKeyID
		} else {
			nextClient.headerKeyID = ""
		}
	} else if resetParams && targetVersion == awg.ProtocolVersion31 {
		nextClient.headerKeyID = m.defaults.DefaultHeaderKeyID
	}
	if targetVersion == awg.ProtocolVersion2 {
		nextClient.headerKeyID = ""
	}
	if replacementHeaderKey != nil {
		nextClient.headerKeyID = replacementHeaderKeyID
	}

	prospective := m.prospectiveData()
	storedIndex := -1
	for index := range prospective.Clients {
		if prospective.Clients[index].ID == client.ID {
			storedIndex = index
			break
		}
	}
	if storedIndex < 0 {
		return nil, fmt.Errorf("prepare client update: client %q missing from storage", client.ID)
	}
	if replacementHeaderKey != nil {
		if prospective.AWG31 == nil || prospective.AWG31.HeaderKeys == nil {
			return nil, errors.New("AWG 3.1 header key state is unavailable")
		}
		prospective.AWG31.HeaderKeys[replacementHeaderKeyID] = *replacementHeaderKey
	}
	prospective.Clients[storedIndex] = cloneClientData(nextClient)
	markAndSweepHeaderKeys(prospective)

	newProfile, err := effectiveProfileForData(m.defaults, prospective, nextClient.ProtocolVersion, nextClient.AWGParams, nextClient.headerKeyID)
	if err != nil {
		return nil, err
	}
	migrationRequired := oldProfile.Key() != newProfile.Key() || oldProfile.Params().Port != newProfile.Params().Port

	commit := func() {
		m.commitClientLocked(nextClient, prospective)
	}

	if !migrationRequired {
		if err := m.storage.Save(prospective); err != nil {
			return nil, persistenceFailure("save client update", err, nil)
		}

		commit()
		result := cloneClientData(nextClient)

		return &result, nil
	}
	if migrationGuard == nil {
		return nil, errors.New("migration guard is required")
	}

	publicKey, err := awg.Base64ToKey(client.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	presharedKey, err := decodePresharedKey(client.PresharedKey)
	if err != nil {
		return nil, err
	}

	transaction := func() error {
		oldPort, err := m.pool.PortForProfile(oldProfile)
		if err != nil {
			return fmt.Errorf("get current port before migration: %w", err)
		}
		if err := m.pool.MigratePeer(oldProfile, newProfile, newProfile.Params().Port, publicKey, presharedKey, client.Address); err != nil {
			return fmt.Errorf("migrate peer: %w", err)
		}
		if err := m.storage.Save(prospective); err != nil {
			rollbackErr := m.pool.MigratePeer(newProfile, oldProfile, oldPort, publicKey, presharedKey, client.Address)

			return persistenceFailure("save client update", err, rollbackErr)
		}

		commit()

		return nil
	}

	if err := migrationGuard(transaction); err != nil {
		return nil, err
	}

	result := cloneClientData(nextClient)

	return &result, nil
}

func (m *Manager) UpdateLANGroup(clientIDs []string, lanGroupID string) ([]ClientData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(clientIDs) == 0 {
		return nil, ErrEmptyClientIDs
	}
	if lanGroupID == "" {
		return nil, ErrEmptyLANGroupID
	}

	storedIndexes := make(map[string]int, len(m.data.Clients))
	for index := range m.data.Clients {
		storedIndexes[m.data.Clients[index].ID] = index
	}

	seen := make(map[string]struct{}, len(clientIDs))
	for _, id := range clientIDs {
		if _, duplicate := seen[id]; duplicate {
			return nil, ErrDuplicateClientID
		}
		seen[id] = struct{}{}
	}
	for _, id := range clientIDs {
		if _, exists := m.clients[id]; !exists {
			return nil, fmt.Errorf("%w: %s", ErrClientNotFound, id)
		}
		if _, exists := storedIndexes[id]; !exists {
			return nil, fmt.Errorf("prepare LAN group update: client %q missing from storage", id)
		}
	}

	prospective := m.prospectiveData()
	for _, id := range clientIDs {
		prospective.Clients[storedIndexes[id]].LANGroupID = lanGroupID
	}

	if err := m.blockLANLocked(); err != nil {
		return nil, err
	}
	if err := m.storage.Save(prospective); err != nil {
		return nil, persistenceFailure("save LAN group update", err, nil)
	}

	m.data = prospective
	result := make([]ClientData, 0, len(clientIDs))
	for _, id := range clientIDs {
		nextClient := cloneClientData(prospective.Clients[storedIndexes[id]])
		m.clients[id] = &nextClient
		result = append(result, cloneClientData(nextClient))
	}
	if err := m.rebuildLANLocked(); err != nil {
		return nil, err
	}

	return result, nil
}

func (m *Manager) DeleteClient(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[id]
	if !ok {
		return ErrClientNotFound
	}

	publicKey, err := awg.Base64ToKey(client.PublicKey)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}
	presharedKey, err := decodePresharedKey(client.PresharedKey)
	if err != nil {
		return err
	}
	profile, err := effectiveProfileForData(m.defaults, m.data, client.ProtocolVersion, client.AWGParams, client.headerKeyID)
	if err != nil {
		return err
	}
	currentPort, err := m.pool.PortForProfile(profile)
	if err != nil {
		return fmt.Errorf("get current port before deletion: %w", err)
	}

	prospective := m.prospectiveData()
	storedIndex := -1
	for index := range prospective.Clients {
		if prospective.Clients[index].ID == id {
			storedIndex = index
			break
		}
	}
	if storedIndex < 0 {
		return fmt.Errorf("prepare client deletion: client %q missing from storage", id)
	}
	prospective.Clients = append(prospective.Clients[:storedIndex], prospective.Clients[storedIndex+1:]...)
	markAndSweepHeaderKeys(prospective)

	if err := m.blockLANLocked(); err != nil {
		return err
	}
	if err := m.pool.RemovePeer(profile, publicKey, client.Address); err != nil {
		return fmt.Errorf("remove peer from device: %w", err)
	}
	if err := m.storage.Save(prospective); err != nil {
		rollbackErr := m.pool.AddPeer(profile, currentPort, publicKey, presharedKey, client.Address)

		return persistenceFailure("save deleted client", err, rollbackErr)
	}

	delete(m.usedIPs, client.Address)
	delete(m.clients, id)
	m.data = prospective
	if err := m.rebuildLANLocked(); err != nil {
		return err
	}

	return nil
}

func (m *Manager) ListClients() []ClientData {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ClientData, 0, len(m.clients))
	for _, client := range m.clients {
		result = append(result, cloneClientData(*client))
	}

	return result
}

func (m *Manager) GetClient(id string) (*ClientData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, ok := m.clients[id]
	if !ok {
		return nil, ErrClientNotFound
	}

	result := cloneClientData(*client)

	return &result, nil
}

func (m *Manager) GetClientConfig(id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, ok := m.clients[id]
	if !ok {
		return "", ErrClientNotFound
	}
	profile, err := effectiveProfileForData(m.defaults, m.data, client.ProtocolVersion, client.AWGParams, client.headerKeyID)
	if err != nil {
		return "", err
	}
	port, err := m.pool.PortForProfile(profile)
	if err != nil {
		return "", fmt.Errorf("get port for params: %w", err)
	}

	return renderClientConfigForProfile(client, profile, m.config.DNS, m.pool.PublicKey(), m.config.Network().String(), m.config.Endpoint, port)
}

func renderClientConfig(client *ClientData, params awg.AWGParams, serverPubKey [32]byte, network, endpoint string, port int) (string, error) {
	return renderClientConfigValues(client, params, awg.ProtocolVersion2, params.ConfigLines(), params.DNS, serverPubKey, network, endpoint, port)
}

func renderClientConfigForProfile(client *ClientData, profile awg.Profile, defaultDNS string, serverPubKey [32]byte, network, endpoint string, port int) (string, error) {
	params := profile.Params()

	return renderClientConfigValues(client, params, profile.Version(), profile.ClientConfigLines(), defaultDNS, serverPubKey, network, endpoint, port)
}

func renderClientConfigValues(client *ClientData, params awg.AWGParams, version awg.ProtocolVersion, profileLines, defaultDNS string, serverPubKey [32]byte, network, endpoint string, port int) (string, error) {
	cfg := fmt.Sprintf(`[Interface]
PrivateKey = %s`, client.PrivateKey)

	if params.ClientListenPort > 0 {
		cfg += fmt.Sprintf(`
ListenPort = %d`, params.ClientListenPort)
	}

	cfg += fmt.Sprintf("\nAddress = %s/32", client.Address)
	if dns, includeDNS := awg.ResolveDNS(client.AWGParams, defaultDNS); includeDNS {
		cfg += fmt.Sprintf("\nDNS = %s", dns)
	}
	cfg += fmt.Sprintf("\nMTU = %d", params.MTU)
	cfg += profileLines

	cfg += fmt.Sprintf(`

[Peer]
PublicKey = %s`, awg.KeyToBase64(serverPubKey))
	if client.PresharedKey != "" {
		cfg += fmt.Sprintf(`
PresharedKey = %s`, client.PresharedKey)
	}

	allowedIPs, err := routingAllowedIPs(client.Routing)
	if err != nil {
		return "", fmt.Errorf("render routing allowed IPs: %w", err)
	}

	cfg += fmt.Sprintf(`
Endpoint = %s:%d
AllowedIPs = %s, %s
PersistentKeepalive = %s`, endpoint, port, network, allowedIPs, params.PersistentKeepaliveConfigValue(version))

	return cfg, nil
}

func (m *Manager) ServerPrivateKey() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.data.ServerPrivateKey
}

func (m *Manager) SetServerPrivateKey(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	prospective := m.prospectiveData()
	prospective.ServerPrivateKey = key
	if err := m.storage.Save(prospective); err != nil {
		return err
	}

	m.data = prospective

	return nil
}

func (m *Manager) commitClientLocked(client ClientData, data *StorageData) {
	copy := cloneClientData(client)
	m.clients[copy.ID] = &copy
	m.usedIPs[copy.Address] = true
	m.data = data
}

func (m *Manager) effectiveProfile(params *awg.AWGParams) (awg.Profile, error) {
	return effectiveProfileForData(m.defaults, m.data, awg.ProtocolVersion2, params, "")
}

func (m *Manager) effectiveParams(params *awg.AWGParams) awg.AWGParams {
	return effectiveParamsForVersion(m.defaults, awg.ProtocolVersion2, params)
}

func effectiveProfileForData(defaults ManagerDefaults, data *StorageData, version awg.ProtocolVersion, params *awg.AWGParams, headerKeyID string) (awg.Profile, error) {
	if !version.Valid() {
		return awg.Profile{}, fmt.Errorf("%w: protocol_version must be 2.0 or 3.1", awg.ErrInvalidParams)
	}
	if err := awg.ValidateOverridesForVersion(version, params); err != nil {
		return awg.Profile{}, err
	}

	effective := effectiveParamsForVersion(defaults, version, params)
	if version == awg.ProtocolVersion2 {
		return awg.NewLegacyProfile(effective)
	}

	headerKey, err := resolveHeaderProtectionKey(data, headerKeyID)
	if err != nil {
		return awg.Profile{}, err
	}

	return awg.NewAWG31Profile(effective, headerKey)
}

func effectiveParamsForVersion(defaults ManagerDefaults, version awg.ProtocolVersion, params *awg.AWGParams) awg.AWGParams {
	base := defaults.LegacyParams
	if version == awg.ProtocolVersion31 {
		base = defaults.AWG31Params
	}

	result := *cloneStoredAWGParams(&base)
	if params == nil {
		return result
	}
	if params.MTU > 0 {
		result.MTU = params.MTU
	}
	if params.DNS != "" {
		result.DNS = params.DNS
		result.DNSMode = ""
		result.DNSServers = nil
	}
	if params.DNSMode != "" {
		result.DNS = ""
		result.DNSMode = params.DNSMode
		result.DNSServers = append([]string(nil), params.DNSServers...)
	}
	if params.PersistentKeepalive != nil {
		result.PersistentKeepalive = cloneStoredRange(params.PersistentKeepalive)
	}
	if params.ContentPaddingAddition != nil {
		result.ContentPaddingAddition = cloneStoredRange(params.ContentPaddingAddition)
	}
	if params.RekeyAfterTime != nil {
		result.RekeyAfterTime = cloneStoredRange(params.RekeyAfterTime)
	}
	if params.RekeyTimeout != nil {
		result.RekeyTimeout = cloneStoredRange(params.RekeyTimeout)
	}
	if params.RejectAfterTime != nil {
		result.RejectAfterTime = cloneStoredRange(params.RejectAfterTime)
	}
	if params.KeepaliveTimeout != nil {
		result.KeepaliveTimeout = cloneStoredRange(params.KeepaliveTimeout)
	}
	if params.MaxHandshakeAttempts != nil {
		result.MaxHandshakeAttempts = cloneStoredRange(params.MaxHandshakeAttempts)
	}
	if params.RandomTrailers != "" {
		result.RandomTrailers = params.RandomTrailers
	}
	if params.DisableCookies != "" {
		result.DisableCookies = params.DisableCookies
	}
	if params.Port > 0 {
		result.Port = params.Port
	}
	if params.ClientListenPort > 0 {
		result.ClientListenPort = params.ClientListenPort
	}
	if params.Jc > 0 {
		result.Jc = params.Jc
	}
	if params.Jmin > 0 {
		result.Jmin = params.Jmin
	}
	if params.Jmax > 0 {
		result.Jmax = params.Jmax
	}
	if params.S1 > 0 {
		result.S1 = params.S1
	}
	if params.S2 > 0 {
		result.S2 = params.S2
	}
	if params.S3 > 0 {
		result.S3 = params.S3
	}
	if params.S4 > 0 {
		result.S4 = params.S4
	}
	if params.H1 != "" {
		result.H1 = params.H1
	}
	if params.H2 != "" {
		result.H2 = params.H2
	}
	if params.H3 != "" {
		result.H3 = params.H3
	}
	if params.H4 != "" {
		result.H4 = params.H4
	}
	if params.I1 != "" {
		result.I1 = params.I1
	}
	if params.I2 != "" {
		result.I2 = params.I2
	}
	if params.I3 != "" {
		result.I3 = params.I3
	}
	if params.I4 != "" {
		result.I4 = params.I4
	}
	if params.I5 != "" {
		result.I5 = params.I5
	}

	return result
}

func resolveManagerDefaults(defaults ManagerDefaults, data *StorageData) (ManagerDefaults, error) {
	if data == nil || data.AWG31 == nil {
		return ManagerDefaults{}, errors.New("AWG 3.1 storage state is required")
	}
	if !defaults.DefaultVersion.Valid() {
		return ManagerDefaults{}, fmt.Errorf("%w: default protocol version must be 2.0 or 3.1", awg.ErrInvalidParams)
	}
	if _, err := awg.NewLegacyProfile(defaults.LegacyParams); err != nil {
		return ManagerDefaults{}, fmt.Errorf("validate default legacy profile: %w", err)
	}
	if err := validateAWG31Storage(data.AWG31); err != nil {
		return ManagerDefaults{}, err
	}

	resolved := cloneManagerDefaults(defaults)
	resolvedAWG31 := awg.ApplyGeneratedParamsV31(&resolved.AWG31Params, *data.AWG31.GeneratedParams)
	resolved.AWG31Params = *resolvedAWG31
	resolved.DefaultHeaderKeyID = data.AWG31.DefaultHeaderKeyID
	headerKey, err := resolveHeaderProtectionKey(data, resolved.DefaultHeaderKeyID)
	if err != nil {
		return ManagerDefaults{}, err
	}
	if _, err := awg.NewAWG31Profile(resolved.AWG31Params, headerKey); err != nil {
		return ManagerDefaults{}, fmt.Errorf("validate default AWG 3.1 profile: %w", err)
	}

	return resolved, nil
}

func validateAWG31Storage(storage *AWG31Storage) error {
	if storage.DefaultHeaderKeyID == "" {
		return errors.New("AWG 3.1 default header key ID is required")
	}
	if storage.GeneratedParams == nil {
		return errors.New("AWG 3.1 generated params are required")
	}
	if len(storage.HeaderKeys) == 0 {
		return errors.New("AWG 3.1 header keys are required")
	}
	if _, ok := storage.HeaderKeys[storage.DefaultHeaderKeyID]; !ok {
		return errors.New("AWG 3.1 default header key is missing")
	}

	for id, keyData := range storage.HeaderKeys {
		if id == "" {
			return errors.New("AWG 3.1 header key ID is required")
		}
		if _, err := awg.Base64ToHeaderProtectionKey(keyData.HeaderProtectionKey); err != nil {
			return errors.New("invalid persisted AWG 3.1 header key")
		}
	}

	return nil
}

func resolveHeaderProtectionKey(data *StorageData, keyID string) (awg.HeaderProtectionKey, error) {
	if data == nil || data.AWG31 == nil || keyID == "" {
		return awg.HeaderProtectionKey{}, errors.New("missing AWG 3.1 header key reference")
	}
	keyData, ok := data.AWG31.HeaderKeys[keyID]
	if !ok {
		return awg.HeaderProtectionKey{}, errors.New("missing AWG 3.1 header key reference")
	}
	key, err := awg.Base64ToHeaderProtectionKey(keyData.HeaderProtectionKey)
	if err != nil {
		return awg.HeaderProtectionKey{}, errors.New("invalid AWG 3.1 header key")
	}

	return key, nil
}

func validateRestoreInterfaces(cfg *config.Config, entries []restoreEntry) error {
	if err := awg.ValidatePort(cfg.ListenPort); err != nil {
		return fmt.Errorf("validate default listen port: %w", err)
	}

	profilePorts := make(map[awg.ProfileKey]int, len(entries))
	usedPorts := make(map[int]struct{}, len(entries))
	nextPort := cfg.ListenPort

	for _, entry := range entries {
		key := entry.profile.Key()
		if actualPort, exists := profilePorts[key]; exists {
			if entry.requestedPort != 0 && entry.requestedPort != actualPort {
				return awg.ErrProfilePortConflict
			}
			continue
		}
		if cfg.MaxInterfaces > 0 && len(profilePorts) >= cfg.MaxInterfaces {
			return awg.ErrMaxInterfacesReached
		}

		port, err := resolveRestorePort(entry.requestedPort, nextPort, usedPorts)
		if err != nil {
			return err
		}
		profilePorts[key] = port
		usedPorts[port] = struct{}{}

		for nextPort <= awg.MaxPort {
			if _, used := usedPorts[nextPort]; !used {
				break
			}
			nextPort++
		}
	}

	return nil
}

func resolveRestorePort(requestedPort, nextPort int, usedPorts map[int]struct{}) (int, error) {
	if err := awg.ValidatePort(requestedPort); err != nil {
		return 0, err
	}
	if requestedPort != 0 {
		if _, used := usedPorts[requestedPort]; used {
			return 0, fmt.Errorf("port %d: %w", requestedPort, awg.ErrPortInUse)
		}

		return requestedPort, nil
	}

	for nextPort <= awg.MaxPort {
		if _, used := usedPorts[nextPort]; !used {
			return nextPort, nil
		}
		nextPort++
	}

	return 0, errors.New("no available ports (exhausted range)")
}

func normalizeStoredAddress(cfg *config.Config, address string) (string, bool, error) {
	parsed := net.ParseIP(address)
	if parsed == nil || parsed.To4() == nil {
		return "", false, errors.New("must be an IPv4 address")
	}
	parsed = parsed.To4()
	if !cfg.Network().Contains(parsed) {
		return "", false, errors.New("must belong to the configured VPN network")
	}
	if parsed.Equal(cfg.ServerIP()) {
		return "", false, errors.New("must not use the server address")
	}

	network := cfg.Network()
	ones, bits := network.Mask.Size()
	if ones == bits {
		return "", false, errors.New("configured VPN network has no client addresses")
	}
	networkAddress := ipToUint32(network.IP)
	broadcastAddress := networkAddress | uint32((1<<(bits-ones))-1)
	addressValue := ipToUint32(parsed)
	if addressValue == networkAddress || addressValue == broadcastAddress {
		return "", false, errors.New("must not use a network or broadcast address")
	}

	canonical := parsed.String()

	return canonical, canonical != address, nil
}

func validateClientID(id string) error {
	if id == "" {
		return errors.New("client ID is required")
	}
	if utf8.RuneCountInString(id) > 256 {
		return errors.New("client ID is too long (max 256 chars)")
	}

	return nil
}

func cloneManagerDefaults(defaults ManagerDefaults) ManagerDefaults {
	clone := defaults
	clone.LegacyParams = *cloneStoredAWGParams(&defaults.LegacyParams)
	clone.AWG31Params = *cloneStoredAWGParams(&defaults.AWG31Params)

	return clone
}

func (m *Manager) newHeaderKeyData() (string, HeaderKeyData, error) {
	if m.data.AWG31 == nil || m.data.AWG31.HeaderKeys == nil {
		return "", HeaderKeyData{}, errors.New("AWG 3.1 header key state is unavailable")
	}
	key, err := awg.GenerateHeaderProtectionKey()
	if err != nil {
		return "", HeaderKeyData{}, fmt.Errorf("generate AWG 3.1 header key: %w", err)
	}

	for attempt := 0; attempt < maxRegenerationAttempts; attempt++ {
		keyID, err := generateHeaderKeyID()
		if err != nil {
			return "", HeaderKeyData{}, err
		}
		if _, exists := m.data.AWG31.HeaderKeys[keyID]; !exists {
			return keyID, HeaderKeyData{HeaderProtectionKey: awg.HeaderProtectionKeyToBase64(key)}, nil
		}
	}

	return "", HeaderKeyData{}, errors.New("failed to generate a unique AWG 3.1 header key ID")
}

func markAndSweepHeaderKeys(data *StorageData) {
	if data == nil || data.AWG31 == nil || data.AWG31.HeaderKeys == nil {
		return
	}

	referenced := make(map[string]struct{})
	for _, client := range data.Clients {
		if client.ProtocolVersion == awg.ProtocolVersion31 && client.headerKeyID != "" {
			referenced[client.headerKeyID] = struct{}{}
		}
	}

	for keyID := range data.AWG31.HeaderKeys {
		if keyID == data.AWG31.DefaultHeaderKeyID {
			continue
		}
		if _, exists := referenced[keyID]; !exists {
			delete(data.AWG31.HeaderKeys, keyID)
		}
	}
}

func defaultLANGroupID(id string) string {
	return "peer:" + id
}

func lanPeers(clients []ClientData) []awg.LANPeer {
	peers := make([]awg.LANPeer, 0, len(clients))
	for _, client := range clients {
		peers = append(peers, awg.LANPeer{Address: client.Address, GroupID: client.LANGroupID})
	}

	return peers
}

func (m *Manager) blockLANLocked() error {
	if err := m.pool.ApplyLANIsolation(nil); err != nil {
		return fmt.Errorf("block inter-client traffic: %w", err)
	}

	return nil
}

func (m *Manager) rebuildLANLocked() error {
	if err := m.pool.ApplyLANIsolation(lanPeers(m.data.Clients)); err != nil {
		return fmt.Errorf("rebuild LAN firewall: %w", err)
	}

	return nil
}

func decodePresharedKey(encoded string) (*[32]byte, error) {
	if encoded == "" {
		return nil, nil
	}
	key, err := awg.Base64ToKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode preshared key: %w", err)
	}

	return &key, nil
}

func (m *Manager) allocateIP() (string, error) {
	network := m.config.Network()
	ones, bits := network.Mask.Size()
	networkAddress := ipToUint32(network.IP)
	broadcastAddress := networkAddress | uint32((1<<(bits-ones))-1)
	serverIP := m.config.ServerIP().String()

	for address := networkAddress + 2; address < broadcastAddress; address++ {
		candidate := uint32ToIP(address).String()
		if candidate == serverIP {
			continue
		}
		if !m.usedIPs[candidate] {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no available IPs in subnet %s", network.String())
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()

	return binary.BigEndian.Uint32(ip)
}

func uint32ToIP(value uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, value)

	return ip
}
