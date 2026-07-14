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

type ClientUpdate struct {
	AWGParams    *awg.AWGParams
	AWGParamsSet bool
	Routing      *Routing
	RoutingSet   bool
}

type Manager struct {
	mu            sync.RWMutex
	pool          *awg.Pool
	storage       *Storage
	config        *config.Config
	defaultParams awg.AWGParams
	clients       map[string]*ClientData
	usedIPs       map[string]bool
	data          *StorageData
}

func NewManager(pool *awg.Pool, storage *Storage, cfg *config.Config, defaultParams awg.AWGParams, data *StorageData) (*Manager, error) {
	m := &Manager{
		pool:          pool,
		storage:       storage,
		config:        cfg,
		defaultParams: defaultParams,
		clients:       make(map[string]*ClientData),
		usedIPs:       make(map[string]bool),
		data:          data,
	}

	var restored []ClientData

	for _, c := range data.Clients {
		pubKey, err := awg.Base64ToKey(c.PublicKey)
		if err != nil {
			log.Printf("skip client %s: invalid public key: %v", c.ID, err)
			continue
		}

		presharedKey, err := decodePresharedKey(c.PresharedKey)
		if err != nil {
			log.Printf("skip client %s: invalid preshared key: %v", c.ID, err)
			continue
		}

		params := m.effectiveParams(c.AWGParams)

		if err := pool.AddPeer(params, pubKey, presharedKey, c.Address); err != nil {
			log.Printf("skip client %s: failed to add peer: %v", c.ID, err)
			continue
		}

		cp := c
		m.clients[c.ID] = &cp

		m.usedIPs[c.Address] = true

		restored = append(restored, c)

		log.Printf("restored client %s (%s)", c.ID, c.Address)
	}

	data.Clients = restored

	log.Printf("loaded %d clients from storage", len(m.clients))

	return m, nil
}

func (m *Manager) CreateClient(name string, params *awg.AWGParams, routing *Routing) (*ClientData, error) {
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

	if err := m.pool.AddPeer(effective, pubKey, &presharedKey, ip); err != nil {
		return nil, fmt.Errorf("add peer to device: %w", err)
	}

	client := &ClientData{
		ID:           name,
		PrivateKey:   awg.KeyToBase64(privKey),
		PublicKey:    awg.KeyToBase64(pubKey),
		PresharedKey: awg.KeyToBase64(presharedKey),
		Address:      ip,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		AWGParams:    params,
		Routing:      normalizedRouting,
	}

	m.clients[client.ID] = client

	m.usedIPs[ip] = true

	m.data.Clients = append(m.data.Clients, *client)

	if err := m.storage.Save(m.data); err != nil {
		log.Printf("warning: failed to save storage: %v", err)
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

func (m *Manager) UpdateClient(id string, update ClientUpdate) (*ClientData, error) {
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

	return m.applyClientUpdateLocked(client, params, routing, update.AWGParamsSet)
}

func (m *Manager) RegenerateAWGParams(id string) (*ClientData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[id]
	if !ok {
		return nil, ErrClientNotFound
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

		return m.applyClientUpdateLocked(client, normalized, client.Routing, true)
	}

	return nil, ErrGeneratedParamsUnchanged
}

func (m *Manager) applyClientUpdateLocked(client *ClientData, params *awg.AWGParams, routing *Routing, awgParamsSet bool) (*ClientData, error) {
	if awgParamsSet {
		normalized, err := awg.NormalizeOverrides(params)
		if err != nil {
			return nil, err
		}

		params = normalized

		oldParams := m.effectiveParams(client.AWGParams)

		newParams, err := m.validatedParams(params)
		if err != nil {
			return nil, err
		}

		if oldParams.Key() != newParams.Key() || oldParams.Port != newParams.Port {
			publicKey, err := awg.Base64ToKey(client.PublicKey)
			if err != nil {
				return nil, fmt.Errorf("decode public key: %w", err)
			}

			presharedKey, err := decodePresharedKey(client.PresharedKey)
			if err != nil {
				return nil, err
			}

			if err := m.pool.MigratePeer(oldParams, newParams, publicKey, presharedKey, client.Address); err != nil {
				return nil, fmt.Errorf("migrate peer: %w", err)
			}
		}
	}

	client.AWGParams = params
	client.Routing = routing

	for i, stored := range m.data.Clients {
		if stored.ID == client.ID {
			m.data.Clients[i] = *client
			break
		}
	}

	if err := m.storage.Save(m.data); err != nil {
		log.Printf("warning: failed to save storage: %v", err)
	}

	result := *client
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

	params := m.effectiveParams(client.AWGParams)

	if err := m.pool.RemovePeer(params, pubKey, client.Address); err != nil {
		return fmt.Errorf("remove peer from device: %w", err)
	}

	delete(m.usedIPs, client.Address)

	delete(m.clients, id)

	newClients := make([]ClientData, 0, len(m.data.Clients)-1)

	for _, c := range m.data.Clients {
		if c.ID != id {
			newClients = append(newClients, c)
		}
	}

	m.data.Clients = newClients

	if err := m.storage.Save(m.data); err != nil {
		log.Printf("warning: failed to save storage: %v", err)
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

	return renderClientConfig(client, params, serverPubKey, m.config.Endpoint, port)
}

func renderClientConfig(client *ClientData, params awg.AWGParams, serverPubKey [32]byte, endpoint string, port int) (string, error) {
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
AllowedIPs = %s
PersistentKeepalive = %d`, endpoint, port, allowedIPs, params.PersistentKeepaliveValue())

	return cfg, nil
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
		return m.defaultParams
	}

	result := m.defaultParams

	if params.MTU > 0 {
		result.MTU = params.MTU
	}

	if params.DNS != "" {
		result.DNS = params.DNS
	}

	if params.PersistentKeepalive != nil {
		result.PersistentKeepalive = params.PersistentKeepalive
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
