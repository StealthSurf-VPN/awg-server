package clients

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

const maxRegenerationAttempts = 8

var ErrClientExists = errors.New("client already exists")
var ErrClientNotFound = errors.New("client not found")
var ErrEmptyClientUpdate = errors.New("at least one of awg_params or routing is required")
var ErrGeneratedParamsUnchanged = errors.New("failed to generate distinct awg params")
var ErrEmptyClientIDs = errors.New("client_ids is required")
var ErrEmptyLANGroupID = errors.New("lan_group_id is required")
var ErrDuplicateClientID = errors.New("client_ids must be unique")

type ClientUpdate struct {
	AWGParams    *awg.AWGParams
	AWGParamsSet bool
	Routing      *Routing
	RoutingSet   bool
}

type MigrationGuard func(func() error) error

type devicePool interface {
	AddPeer(awg.AWGParams, [32]byte, *[32]byte, string) error
	RemovePeer(awg.AWGParams, [32]byte, string) error
	MigratePeer(awg.AWGParams, awg.AWGParams, [32]byte, *[32]byte, string) error
	PortForParams(awg.AWGParams) (int, error)
	PublicKey() [32]byte
	ApplyLANIsolation([]awg.LANPeer) error
}

type Manager struct {
	mu            sync.RWMutex
	pool          devicePool
	storage       *Storage
	config        *config.Config
	defaultParams awg.AWGParams
	clients       map[string]*ClientData
	usedIPs       map[string]bool
	data          *StorageData
}

func (m *Manager) prospectiveData() StorageData {
	data := *m.data
	data.Clients = append([]ClientData(nil), m.data.Clients...)

	return data
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

func NewManager(pool devicePool, storage *Storage, cfg *config.Config, defaultParams awg.AWGParams, data *StorageData) (*Manager, error) {
	m := &Manager{
		pool:          pool,
		storage:       storage,
		config:        cfg,
		defaultParams: defaultParams,
		clients:       make(map[string]*ClientData),
		usedIPs:       make(map[string]bool),
		data:          data,
	}

	legacyGroups := false

	for i := range data.Clients {
		c := &data.Clients[i]
		if c.LANGroupID == "" {
			c.LANGroupID = defaultLANGroupID(c.ID)
			legacyGroups = true
		}

		pubKey, err := awg.Base64ToKey(c.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: decode public key: %w", c.ID, err)
		}

		privateKey, err := awg.Base64ToKey(c.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: decode private key: %w", c.ID, err)
		}
		if awg.PublicKeyFromPrivate(privateKey) != pubKey {
			return nil, fmt.Errorf("restore client %q: private and public keys do not match", c.ID)
		}

		presharedKey, err := decodePresharedKey(c.PresharedKey)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: %w", c.ID, err)
		}

		params, err := m.validatedParams(c.AWGParams)
		if err != nil {
			return nil, fmt.Errorf("restore client %q: validate awg params: %w", c.ID, err)
		}
		if _, err := NormalizeRouting(c.Routing); err != nil {
			return nil, fmt.Errorf("restore client %q: validate routing: %w", c.ID, err)
		}

		if err := pool.AddPeer(params, pubKey, presharedKey, c.Address); err != nil {
			return nil, fmt.Errorf("restore client %q: add peer: %w", c.ID, err)
		}

		cp := *c
		m.clients[c.ID] = &cp

		m.usedIPs[c.Address] = true

		log.Printf("restored client %s (%s)", c.ID, c.Address)
	}
	if legacyGroups {
		if err := storage.Save(data); err != nil {
			return nil, fmt.Errorf("persist legacy LAN groups: %w", err)
		}
	}
	if err := pool.ApplyLANIsolation(lanPeers(data.Clients)); err != nil {
		return nil, fmt.Errorf("restore LAN firewall: %w", err)
	}

	log.Printf("loaded %d clients from storage", len(m.clients))

	return m, nil
}

func (m *Manager) CreateClient(name string, params *awg.AWGParams, routing *Routing, lanGroupID string) (*ClientData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	normalizedParams, err := awg.NormalizeOverrides(params)
	if err != nil {
		return nil, err
	}

	params = normalizedParams

	normalizedRouting, err := NormalizeRouting(routing)
	if err != nil {
		return nil, err
	}

	if _, exists := m.clients[name]; exists {
		return nil, ErrClientExists
	}
	if lanGroupID == "" {
		lanGroupID = defaultLANGroupID(name)
	}

	effective, err := m.validatedParams(params)
	if err != nil {
		return nil, err
	}

	privKey, err := awg.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key pair: %w", err)
	}

	pubKey := awg.PublicKeyFromPrivate(privKey)

	presharedKey, err := awg.GeneratePresharedKey()
	if err != nil {
		return nil, fmt.Errorf("generate preshared key: %w", err)
	}

	ip, err := m.allocateIP()
	if err != nil {
		return nil, fmt.Errorf("allocate IP: %w", err)
	}

	client := &ClientData{
		ID:           name,
		PrivateKey:   awg.KeyToBase64(privKey),
		PublicKey:    awg.KeyToBase64(pubKey),
		PresharedKey: awg.KeyToBase64(presharedKey),
		Address:      ip,
		LANGroupID:   lanGroupID,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		AWGParams:    params,
		Routing:      normalizedRouting,
	}

	prospective := m.prospectiveData()
	prospective.Clients = append(prospective.Clients, *client)

	if err := m.blockLANLocked(); err != nil {
		return nil, err
	}

	if err := m.pool.AddPeer(effective, pubKey, &presharedKey, ip); err != nil {
		return nil, fmt.Errorf("add peer to device: %w", err)
	}

	if err := m.storage.Save(&prospective); err != nil {
		rollbackErr := m.pool.RemovePeer(effective, pubKey, ip)

		return nil, persistenceFailure("save created client", err, rollbackErr)
	}

	m.clients[client.ID] = client
	m.usedIPs[ip] = true
	*m.data = prospective
	if err := m.rebuildLANLocked(); err != nil {
		return nil, err
	}

	cp := *client
	return &cp, nil
}

func resolveClientUpdate(client *ClientData, update ClientUpdate) (*awg.AWGParams, *Routing, error) {
	if !update.AWGParamsSet && !update.RoutingSet {
		return nil, nil, ErrEmptyClientUpdate
	}

	params := client.AWGParams
	if update.AWGParamsSet {
		params = update.AWGParams
	}

	routing := client.Routing
	if update.RoutingSet {
		normalizedRouting, err := NormalizeRouting(update.Routing)
		if err != nil {
			return nil, nil, err
		}

		routing = normalizedRouting
	}

	return params, routing, nil
}

func (m *Manager) UpdateClient(id string, update ClientUpdate, migrationGuard MigrationGuard) (*ClientData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[id]
	if !ok {
		return nil, ErrClientNotFound
	}

	params, routing, err := resolveClientUpdate(client, update)
	if err != nil {
		return nil, err
	}

	return m.applyClientUpdateLocked(client, params, routing, update.AWGParamsSet, migrationGuard)
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

	oldKey := m.effectiveParams(client.AWGParams).Key()

	for attempt := 0; attempt < maxRegenerationAttempts; attempt++ {
		generated, err := awg.GenerateParams()
		if err != nil {
			return nil, fmt.Errorf("generate awg params: %w", err)
		}

		candidate := awg.ApplyGeneratedParams(client.AWGParams, *generated)

		normalized, err := awg.NormalizeOverrides(candidate)
		if err != nil {
			return nil, err
		}

		effective, err := m.validatedParams(normalized)
		if err != nil {
			return nil, err
		}

		if effective.Key() == oldKey {
			continue
		}

		return m.applyClientUpdateLocked(client, normalized, client.Routing, true, migrationGuard)
	}

	return nil, ErrGeneratedParamsUnchanged
}

func (m *Manager) applyClientUpdateLocked(client *ClientData, params *awg.AWGParams, routing *Routing, awgParamsSet bool, migrationGuard MigrationGuard) (*ClientData, error) {
	oldParams := m.effectiveParams(client.AWGParams)
	newParams := oldParams
	migrationRequired := false

	if awgParamsSet {
		normalized, err := awg.NormalizeOverrides(params)
		if err != nil {
			return nil, err
		}

		params = normalized

		newParams, err = m.validatedParams(params)
		if err != nil {
			return nil, err
		}

		migrationRequired = oldParams.Key() != newParams.Key() || oldParams.Port != newParams.Port
	}

	nextClient := *client
	nextClient.AWGParams = params
	nextClient.Routing = routing

	prospective := m.prospectiveData()
	storedClientFound := false

	for i, stored := range prospective.Clients {
		if stored.ID == client.ID {
			prospective.Clients[i] = nextClient
			storedClientFound = true
			break
		}
	}

	if !storedClientFound {
		return nil, fmt.Errorf("prepare client update: client %q missing from storage", client.ID)
	}

	commit := func() {
		m.clients[client.ID] = &nextClient
		*m.data = prospective
	}

	if !migrationRequired {
		if err := m.storage.Save(&prospective); err != nil {
			return nil, persistenceFailure("save client update", err, nil)
		}

		commit()

		result := nextClient
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
		oldPort, err := m.pool.PortForParams(oldParams)
		if err != nil {
			return fmt.Errorf("get current port before migration: %w", err)
		}

		rollbackParams := oldParams
		rollbackParams.Port = oldPort

		if err := m.pool.MigratePeer(oldParams, newParams, publicKey, presharedKey, client.Address); err != nil {
			return fmt.Errorf("migrate peer: %w", err)
		}

		if err := m.storage.Save(&prospective); err != nil {
			rollbackErr := m.pool.MigratePeer(newParams, rollbackParams, publicKey, presharedKey, client.Address)

			return persistenceFailure("save client update", err, rollbackErr)
		}

		commit()

		return nil
	}

	err = migrationGuard(transaction)

	if err != nil {
		return nil, err
	}

	result := nextClient
	return &result, nil
}

func (m *Manager) ListClients() []ClientData {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ClientData, 0, len(m.clients))

	for _, c := range m.clients {
		result = append(result, *c)
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

	cp := *client
	return &cp, nil
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
	for i := range m.data.Clients {
		storedIndexes[m.data.Clients[i].ID] = i
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
	if err := m.storage.Save(&prospective); err != nil {
		return nil, persistenceFailure("save LAN group update", err, nil)
	}

	for _, id := range clientIDs {
		nextClient := prospective.Clients[storedIndexes[id]]
		m.clients[id] = &nextClient
	}
	*m.data = prospective

	result := make([]ClientData, 0, len(clientIDs))
	for _, id := range clientIDs {
		result = append(result, *m.clients[id])
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

	pubKey, err := awg.Base64ToKey(client.PublicKey)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}

	presharedKey, err := decodePresharedKey(client.PresharedKey)
	if err != nil {
		return err
	}

	params := m.effectiveParams(client.AWGParams)
	currentPort, err := m.pool.PortForParams(params)
	if err != nil {
		return fmt.Errorf("get current port before deletion: %w", err)
	}

	rollbackParams := params
	rollbackParams.Port = currentPort

	prospective := m.prospectiveData()
	newClients := make([]ClientData, 0, len(prospective.Clients))
	storedClientFound := false

	for _, stored := range prospective.Clients {
		if stored.ID == id {
			storedClientFound = true
			continue
		}

		newClients = append(newClients, stored)
	}

	if !storedClientFound {
		return fmt.Errorf("prepare client deletion: client %q missing from storage", id)
	}

	prospective.Clients = newClients

	if err := m.blockLANLocked(); err != nil {
		return err
	}

	if err := m.pool.RemovePeer(params, pubKey, client.Address); err != nil {
		return fmt.Errorf("remove peer from device: %w", err)
	}

	if err := m.storage.Save(&prospective); err != nil {
		rollbackErr := m.pool.AddPeer(rollbackParams, pubKey, presharedKey, client.Address)

		return persistenceFailure("save deleted client", err, rollbackErr)
	}

	delete(m.usedIPs, client.Address)
	delete(m.clients, id)
	*m.data = prospective
	if err := m.rebuildLANLocked(); err != nil {
		return err
	}

	return nil
}

func (m *Manager) GetClientConfig(id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, ok := m.clients[id]
	if !ok {
		return "", ErrClientNotFound
	}

	params := m.effectiveParams(client.AWGParams)

	serverPubKey := m.pool.PublicKey()

	port, err := m.pool.PortForParams(params)
	if err != nil {
		return "", fmt.Errorf("get port for params: %w", err)
	}

	return renderClientConfig(client, params, serverPubKey, m.config.Network().String(), m.config.Endpoint, port)
}

func renderClientConfig(client *ClientData, params awg.AWGParams, serverPubKey [32]byte, network, endpoint string, port int) (string, error) {
	cfg := fmt.Sprintf(`[Interface]
PrivateKey = %s`, client.PrivateKey)

	if params.ClientListenPort > 0 {
		cfg += fmt.Sprintf(`
ListenPort = %d`, params.ClientListenPort)
	}

	cfg += fmt.Sprintf("\nAddress = %s/32", client.Address)
	if dns, includeDNS := awg.ResolveDNS(client.AWGParams, params.DNS); includeDNS {
		cfg += fmt.Sprintf("\nDNS = %s", dns)
	}
	cfg += fmt.Sprintf("\nMTU = %d", params.MTU)

	cfg += params.ConfigLines()

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
PersistentKeepalive = %s`, endpoint, port, network, allowedIPs, params.PersistentKeepaliveConfigValue(awg.ProtocolVersion2))

	return cfg, nil
}

func defaultLANGroupID(id string) string {
	return "peer:" + id
}

func lanPeers(clients []ClientData) []awg.LANPeer {
	peers := make([]awg.LANPeer, 0, len(clients))

	for _, client := range clients {
		peers = append(peers, awg.LANPeer{
			Address: client.Address,
			GroupID: client.LANGroupID,
		})
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

func (m *Manager) effectiveParams(params *awg.AWGParams) awg.AWGParams {
	if params == nil {
		return cloneEffectiveParams(m.defaultParams)
	}

	result := cloneEffectiveParams(m.defaultParams)

	if params.MTU > 0 {
		result.MTU = params.MTU
	}

	if params.DNS != "" {
		result.DNS = params.DNS
	}

	if params.PersistentKeepalive != nil {
		result.PersistentKeepalive = cloneUint16Range(params.PersistentKeepalive)
	}

	if params.ContentPaddingAddition != nil {
		result.ContentPaddingAddition = cloneUint16Range(params.ContentPaddingAddition)
	}

	if params.RekeyAfterTime != nil {
		result.RekeyAfterTime = cloneUint16Range(params.RekeyAfterTime)
	}

	if params.RekeyTimeout != nil {
		result.RekeyTimeout = cloneUint16Range(params.RekeyTimeout)
	}

	if params.RejectAfterTime != nil {
		result.RejectAfterTime = cloneUint16Range(params.RejectAfterTime)
	}

	if params.KeepaliveTimeout != nil {
		result.KeepaliveTimeout = cloneUint16Range(params.KeepaliveTimeout)
	}

	if params.MaxHandshakeAttempts != nil {
		result.MaxHandshakeAttempts = cloneUint16Range(params.MaxHandshakeAttempts)
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

func (m *Manager) validatedParams(params *awg.AWGParams) (awg.AWGParams, error) {
	if err := awg.ValidateOverrides(params); err != nil {
		return awg.AWGParams{}, err
	}

	effective := m.effectiveParams(params)
	if err := awg.ValidateProfile(effective); err != nil {
		return awg.AWGParams{}, err
	}

	return effective, nil
}

func cloneEffectiveParams(params awg.AWGParams) awg.AWGParams {
	clone := params
	if params.DNSServers != nil {
		clone.DNSServers = append([]string(nil), params.DNSServers...)
	}

	clone.PersistentKeepalive = cloneUint16Range(params.PersistentKeepalive)
	clone.ContentPaddingAddition = cloneUint16Range(params.ContentPaddingAddition)
	clone.RekeyAfterTime = cloneUint16Range(params.RekeyAfterTime)
	clone.RekeyTimeout = cloneUint16Range(params.RekeyTimeout)
	clone.RejectAfterTime = cloneUint16Range(params.RejectAfterTime)
	clone.KeepaliveTimeout = cloneUint16Range(params.KeepaliveTimeout)
	clone.MaxHandshakeAttempts = cloneUint16Range(params.MaxHandshakeAttempts)

	return clone
}

func cloneUint16Range(value *config.Uint16Range) *config.Uint16Range {
	if value == nil {
		return nil
	}

	clone := *value

	return &clone
}

func (m *Manager) allocateIP() (string, error) {
	network := m.config.Network()

	serverIP := m.config.ServerIP()

	ones, bits := network.Mask.Size()

	networkAddr := ipToUint32(network.IP)

	broadcastAddr := networkAddr | uint32((1<<(bits-ones))-1)

	start := networkAddr + 2

	for addr := start; addr < broadcastAddr; addr++ {
		candidate := uint32ToIP(addr)

		candidateStr := candidate.String()

		if candidateStr == serverIP.String() {
			continue
		}

		if !m.usedIPs[candidateStr] {
			return candidateStr, nil
		}
	}

	return "", fmt.Errorf("no available IPs in subnet %s", network.String())
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	return binary.BigEndian.Uint32(ip)
}

func uint32ToIP(n uint32) net.IP {
	ip := make(net.IP, 4)

	binary.BigEndian.PutUint32(ip, n)

	return ip
}

func (m *Manager) ServerPrivateKey() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.data.ServerPrivateKey
}

func (m *Manager) SetServerPrivateKey(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data.ServerPrivateKey = key
	return m.storage.Save(m.data)
}
