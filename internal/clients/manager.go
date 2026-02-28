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

var ErrClientExists = errors.New("client already exists")
var ErrClientNotFound = errors.New("client not found")

type Manager struct {
	mu      sync.RWMutex
	device  *awg.Device
	storage *Storage
	config  *config.Config
	clients map[string]*ClientData
	usedIPs map[string]bool
	data    *StorageData
}

func NewManager(device *awg.Device, storage *Storage, cfg *config.Config) (*Manager, error) {
	data, err := storage.Load()
	if err != nil {
		return nil, fmt.Errorf("load storage: %w", err)
	}

	m := &Manager{
		device:  device,
		storage: storage,
		config:  cfg,
		clients: make(map[string]*ClientData),
		usedIPs: make(map[string]bool),
		data:    data,
	}

	for _, c := range data.Clients {
		pubKey, err := awg.Base64ToKey(c.PublicKey)
		if err != nil {
			log.Printf("skip client %s: invalid public key: %v", c.ID, err)
			continue
		}

		if err := device.AddPeer(pubKey, c.Address); err != nil {
			log.Printf("skip client %s: failed to add peer: %v", c.ID, err)
			continue
		}

		cp := c
		m.clients[c.ID] = &cp

		m.usedIPs[c.Address] = true

		log.Printf("restored client %s (%s)", c.ID, c.Address)
	}

	log.Printf("loaded %d clients from storage", len(m.clients))

	return m, nil
}

func (m *Manager) CreateClient(name string) (*ClientData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clients[name]; exists {
		return nil, ErrClientExists
	}

	privKey, err := awg.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key pair: %w", err)
	}

	pubKey := awg.PublicKeyFromPrivate(privKey)

	ip, err := m.allocateIP()
	if err != nil {
		return nil, fmt.Errorf("allocate IP: %w", err)
	}

	if err := m.device.AddPeer(pubKey, ip); err != nil {
		return nil, fmt.Errorf("add peer to device: %w", err)
	}

	client := &ClientData{
		ID:         name,
		Name:       name,
		PrivateKey: awg.KeyToBase64(privKey),
		PublicKey:  awg.KeyToBase64(pubKey),
		Address:    ip,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	m.clients[client.ID] = client

	m.usedIPs[ip] = true

	m.data.Clients = append(m.data.Clients, *client)

	if err := m.storage.Save(m.data); err != nil {
		log.Printf("warning: failed to save storage: %v", err)
	}

	return client, nil
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

	return client, nil
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

	if err := m.device.RemovePeer(pubKey); err != nil {
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

	serverPubKey := m.device.PublicKey()

	cfg := fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32
DNS = %s
MTU = %d`, client.PrivateKey, client.Address, m.config.DNS, m.config.MTU)

	if m.config.Jc > 0 {
		cfg += fmt.Sprintf("\nJc = %d", m.config.Jc)
	}

	if m.config.Jmin > 0 {
		cfg += fmt.Sprintf("\nJmin = %d", m.config.Jmin)
	}

	if m.config.Jmax > 0 {
		cfg += fmt.Sprintf("\nJmax = %d", m.config.Jmax)
	}

	if m.config.S1 > 0 {
		cfg += fmt.Sprintf("\nS1 = %d", m.config.S1)
	}

	if m.config.S2 > 0 {
		cfg += fmt.Sprintf("\nS2 = %d", m.config.S2)
	}

	if m.config.S3 > 0 {
		cfg += fmt.Sprintf("\nS3 = %d", m.config.S3)
	}

	if m.config.S4 > 0 {
		cfg += fmt.Sprintf("\nS4 = %d", m.config.S4)
	}

	if m.config.H1 > 0 {
		cfg += fmt.Sprintf("\nH1 = %d", m.config.H1)
	}

	if m.config.H2 > 0 {
		cfg += fmt.Sprintf("\nH2 = %d", m.config.H2)
	}

	if m.config.H3 > 0 {
		cfg += fmt.Sprintf("\nH3 = %d", m.config.H3)
	}

	if m.config.H4 > 0 {
		cfg += fmt.Sprintf("\nH4 = %d", m.config.H4)
	}

	if m.config.I1 != "" {
		cfg += fmt.Sprintf("\nI1 = %s", m.config.I1)
	}

	if m.config.I2 != "" {
		cfg += fmt.Sprintf("\nI2 = %s", m.config.I2)
	}

	if m.config.I3 != "" {
		cfg += fmt.Sprintf("\nI3 = %s", m.config.I3)
	}

	if m.config.I4 != "" {
		cfg += fmt.Sprintf("\nI4 = %s", m.config.I4)
	}

	if m.config.I5 != "" {
		cfg += fmt.Sprintf("\nI5 = %s", m.config.I5)
	}

	cfg += fmt.Sprintf(`

[Peer]
PublicKey = %s
Endpoint = %s:%d
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25`, awg.KeyToBase64(serverPubKey), m.config.Endpoint, m.config.ListenPort)

	return cfg, nil
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
	return m.data.ServerPrivateKey
}

func (m *Manager) SetServerPrivateKey(key string) error {
	m.data.ServerPrivateKey = key
	return m.storage.Save(m.data)
}
