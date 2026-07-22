package clients

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

type managerTestPool struct {
	events          []string
	firewallCalls   [][]awg.LANPeer
	activeLANPeers  []awg.LANPeer
	firewallErrAt   int
	firewallCallNum int
	addErr          error
	removeErr       error
}

func (p *managerTestPool) AddPeer(_ awg.AWGParams, _ [32]byte, _ *[32]byte, address string) error {
	p.events = append(p.events, "add:"+address)
	return p.addErr
}

func (p *managerTestPool) RemovePeer(_ awg.AWGParams, _ [32]byte, address string) error {
	p.events = append(p.events, "remove:"+address)
	return p.removeErr
}

func (p *managerTestPool) MigratePeer(_, _ awg.AWGParams, _ [32]byte, _ *[32]byte, _ string) error {
	return nil
}

func (p *managerTestPool) PortForParams(awg.AWGParams) (int, error) {
	return 51820, nil
}

func (p *managerTestPool) PublicKey() [32]byte {
	return [32]byte{1}
}

func (p *managerTestPool) ApplyLANIsolation(peers []awg.LANPeer) error {
	copyPeers := append([]awg.LANPeer(nil), peers...)
	p.firewallCalls = append(p.firewallCalls, copyPeers)
	p.events = append(p.events, fmt.Sprintf("firewall:%d", len(peers)))
	p.firewallCallNum++

	if p.firewallErrAt == p.firewallCallNum {
		return errors.New("firewall unavailable")
	}

	p.activeLANPeers = copyPeers
	return nil
}

func (p *managerTestPool) reset() {
	p.events = nil
	p.firewallCalls = nil
	p.firewallCallNum = 0
	p.firewallErrAt = 0
	p.addErr = nil
	p.removeErr = nil
}

func TestNewManagerPersistsLegacyLANGroups(t *testing.T) {
	data := &StorageData{Clients: []ClientData{
		validStoredClient(t, "one", "10.100.0.2", ""),
		validStoredClient(t, "two", "10.100.0.3", ""),
	}}

	manager, pool, storage := newManagerTest(t, data)

	one, err := manager.GetClient("one")
	if err != nil {
		t.Fatalf("GetClient(one) error = %v", err)
	}
	if one.LANGroupID != "peer:one" {
		t.Fatalf("one LANGroupID = %q, want %q", one.LANGroupID, "peer:one")
	}

	stored, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.Clients[0].LANGroupID != "peer:one" || stored.Clients[1].LANGroupID != "peer:two" {
		t.Fatalf("stored LAN groups = %+v", stored.Clients)
	}
	if len(pool.activeLANPeers) != 2 {
		t.Fatalf("active LAN peers = %+v, want 2", pool.activeLANPeers)
	}
}

func TestCreateClientDefaultsGroupAndGatesFirewall(t *testing.T) {
	manager, pool, storage := newManagerTest(t, &StorageData{})
	pool.reset()

	client, err := manager.CreateClient("device", nil, nil, "")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if client.LANGroupID != "peer:device" {
		t.Fatalf("LANGroupID = %q, want %q", client.LANGroupID, "peer:device")
	}

	wantEvents := []string{"firewall:0", "add:10.100.0.2", "firewall:1"}
	if fmt.Sprint(pool.events) != fmt.Sprint(wantEvents) {
		t.Fatalf("events = %v, want %v", pool.events, wantEvents)
	}

	stored, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(stored.Clients) != 1 || stored.Clients[0].LANGroupID != "peer:device" {
		t.Fatalf("stored clients = %+v", stored.Clients)
	}
}

func TestCreateClientAddPeerFailureLeavesLANBlocked(t *testing.T) {
	manager, pool, _ := newManagerTest(t, &StorageData{})
	pool.reset()
	pool.addErr = errors.New("add unavailable")

	_, err := manager.CreateClient("device", nil, nil, "")
	if err == nil || !strings.Contains(err.Error(), "add unavailable") {
		t.Fatalf("CreateClient() error = %v, want add error", err)
	}
	if len(pool.activeLANPeers) != 0 {
		t.Fatalf("active LAN peers = %+v, want fail-closed empty chain", pool.activeLANPeers)
	}
	if _, err := manager.GetClient("device"); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("GetClient() error = %v, want ErrClientNotFound", err)
	}
}

func TestCreateClientSaveFailureLeavesLANBlockedAndUncommitted(t *testing.T) {
	manager, pool, storage := newManagerTest(t, &StorageData{})
	pool.reset()
	storage.filePath = t.TempDir()

	_, err := manager.CreateClient("device", nil, nil, "")
	if err == nil || !strings.Contains(err.Error(), "save created client") {
		t.Fatalf("CreateClient() error = %v, want storage error", err)
	}
	if len(pool.activeLANPeers) != 0 {
		t.Fatalf("active LAN peers = %+v, want fail-closed empty chain", pool.activeLANPeers)
	}
	if _, err := manager.GetClient("device"); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("GetClient() error = %v, want ErrClientNotFound", err)
	}
}

func TestCreateClientFirewallFailureLeavesOutageAfterCommit(t *testing.T) {
	manager, pool, storage := newManagerTest(t, &StorageData{})
	pool.reset()
	pool.firewallErrAt = 2

	_, err := manager.CreateClient("device", nil, nil, "peer:primary")
	if err == nil || !strings.Contains(err.Error(), "firewall unavailable") {
		t.Fatalf("CreateClient() error = %v, want firewall error", err)
	}
	if len(pool.activeLANPeers) != 0 {
		t.Fatalf("active LAN peers = %+v, want fail-closed empty chain", pool.activeLANPeers)
	}
	if _, err := manager.GetClient("device"); err != nil {
		t.Fatalf("committed client missing after firewall rebuild failure: %v", err)
	}

	stored, loadErr := storage.Load()
	if loadErr != nil {
		t.Fatalf("Load() error = %v", loadErr)
	}
	if len(stored.Clients) != 1 || stored.Clients[0].LANGroupID != "peer:primary" {
		t.Fatalf("stored clients = %+v", stored.Clients)
	}
}

func TestUpdateLANGroupValidatesAllIDsBeforeMutation(t *testing.T) {
	data := &StorageData{Clients: []ClientData{
		validStoredClient(t, "one", "10.100.0.2", "peer:one"),
		validStoredClient(t, "two", "10.100.0.3", "peer:two"),
	}}
	manager, pool, _ := newManagerTest(t, data)
	pool.reset()

	_, err := manager.UpdateLANGroup([]string{"one", "missing"}, "peer:one")
	if !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("UpdateLANGroup() error = %v, want ErrClientNotFound", err)
	}
	if len(pool.firewallCalls) != 0 {
		t.Fatalf("firewall calls = %+v, want none", pool.firewallCalls)
	}

	one, _ := manager.GetClient("one")
	two, _ := manager.GetClient("two")
	if one.LANGroupID != "peer:one" || two.LANGroupID != "peer:two" {
		t.Fatalf("LAN groups changed after failed validation: one=%q two=%q", one.LANGroupID, two.LANGroupID)
	}
}

func TestUpdateLANGroupCommitsBatchUnderFailClosedFirewall(t *testing.T) {
	data := &StorageData{Clients: []ClientData{
		validStoredClient(t, "one", "10.100.0.2", "peer:one"),
		validStoredClient(t, "two", "10.100.0.3", "peer:two"),
	}}
	manager, pool, storage := newManagerTest(t, data)
	pool.reset()

	updated, err := manager.UpdateLANGroup([]string{"two", "one"}, "peer:one")
	if err != nil {
		t.Fatalf("UpdateLANGroup() error = %v", err)
	}
	if len(updated) != 2 || updated[0].ID != "two" || updated[1].ID != "one" {
		t.Fatalf("updated clients = %+v", updated)
	}
	if updated[0].LANGroupID != "peer:one" || updated[1].LANGroupID != "peer:one" {
		t.Fatalf("updated LAN groups = %+v", updated)
	}

	wantEvents := []string{"firewall:0", "firewall:2"}
	if fmt.Sprint(pool.events) != fmt.Sprint(wantEvents) {
		t.Fatalf("events = %v, want %v", pool.events, wantEvents)
	}

	stored, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, client := range stored.Clients {
		if client.LANGroupID != "peer:one" {
			t.Fatalf("stored client = %+v, want group peer:one", client)
		}
	}
}

func TestUpdateLANGroupSaveFailureLeavesLANBlockedAndBatchUnchanged(t *testing.T) {
	data := &StorageData{Clients: []ClientData{
		validStoredClient(t, "one", "10.100.0.2", "peer:one"),
		validStoredClient(t, "two", "10.100.0.3", "peer:two"),
	}}
	manager, pool, storage := newManagerTest(t, data)
	pool.reset()
	storage.filePath = t.TempDir()

	_, err := manager.UpdateLANGroup([]string{"one", "two"}, "peer:one")
	if err == nil || !strings.Contains(err.Error(), "save LAN group update") {
		t.Fatalf("UpdateLANGroup() error = %v, want storage error", err)
	}
	if len(pool.activeLANPeers) != 0 {
		t.Fatalf("active LAN peers = %+v, want fail-closed empty chain", pool.activeLANPeers)
	}

	one, _ := manager.GetClient("one")
	two, _ := manager.GetClient("two")
	if one.LANGroupID != "peer:one" || two.LANGroupID != "peer:two" {
		t.Fatalf("LAN groups changed after failed save: one=%q two=%q", one.LANGroupID, two.LANGroupID)
	}
}

func TestDeleteClientGatesFirewallBeforePeerRemoval(t *testing.T) {
	data := &StorageData{Clients: []ClientData{
		validStoredClient(t, "one", "10.100.0.2", "peer:one"),
	}}
	manager, pool, _ := newManagerTest(t, data)
	pool.reset()

	if err := manager.DeleteClient("one"); err != nil {
		t.Fatalf("DeleteClient() error = %v", err)
	}

	wantEvents := []string{"firewall:0", "remove:10.100.0.2", "firewall:0"}
	if fmt.Sprint(pool.events) != fmt.Sprint(wantEvents) {
		t.Fatalf("events = %v, want %v", pool.events, wantEvents)
	}
}

func TestDeleteClientRemovePeerFailureLeavesLANBlockedAndClientCommitted(t *testing.T) {
	data := &StorageData{Clients: []ClientData{
		validStoredClient(t, "one", "10.100.0.2", "peer:one"),
	}}
	manager, pool, _ := newManagerTest(t, data)
	pool.reset()
	pool.removeErr = errors.New("remove unavailable")

	err := manager.DeleteClient("one")
	if err == nil || !strings.Contains(err.Error(), "remove unavailable") {
		t.Fatalf("DeleteClient() error = %v, want remove error", err)
	}
	if len(pool.activeLANPeers) != 0 {
		t.Fatalf("active LAN peers = %+v, want fail-closed empty chain", pool.activeLANPeers)
	}
	if _, err := manager.GetClient("one"); err != nil {
		t.Fatalf("GetClient() error = %v, want committed client", err)
	}
}

func TestRenderClientConfigPrependsVPNNetwork(t *testing.T) {
	routings := []struct {
		name    string
		routing *Routing
	}{
		{name: "full"},
		{name: "split", routing: &Routing{Mode: RoutingModeSplit, AllowedIPs: []string{"8.8.8.8/32"}}},
		{name: "bypass", routing: &Routing{Mode: RoutingModeBypass, ExcludedIPs: []string{"192.0.2.0/24"}}},
	}

	for _, tt := range routings {
		t.Run(tt.name, func(t *testing.T) {
			client := &ClientData{
				PrivateKey: "private",
				Address:    "10.100.0.2",
				Routing:    tt.routing,
			}
			params := awg.AWGParams{MTU: 1420}

			configuration, err := renderClientConfig(client, params, [32]byte{}, "10.100.0.0/24", "vpn.example.test", 51820)
			if err != nil {
				t.Fatalf("renderClientConfig() error = %v", err)
			}
			if !strings.Contains(configuration, "AllowedIPs = 10.100.0.0/24, ") {
				t.Fatalf("configuration missing explicit VPN network:\n%s", configuration)
			}
		})
	}
}

func newManagerTest(t *testing.T, data *StorageData) (*Manager, *managerTestPool, *Storage) {
	t.Helper()

	cfg := &config.Config{
		Address:    "10.100.0.1/24",
		Endpoint:   "vpn.example.test",
		ListenPort: 51820,
		MTU:        1420,
		DNS:        "1.1.1.1",
		DataDir:    t.TempDir(),
	}
	params := awg.AWGParams{
		MTU: 1420, DNS: "1.1.1.1",
		Jc: 5, Jmin: 50, Jmax: 1000,
		S1: 15, S2: 72,
		H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
	}
	storage := NewStorage(cfg.DataDir)
	if err := storage.Save(data); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	pool := &managerTestPool{}
	manager, err := NewManager(pool, storage, cfg, params, data)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	return manager, pool, storage
}

func validStoredClient(t *testing.T, id, address, lanGroupID string) ClientData {
	t.Helper()

	privateKey, err := awg.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error = %v", err)
	}
	presharedKey, err := awg.GeneratePresharedKey()
	if err != nil {
		t.Fatalf("GeneratePresharedKey() error = %v", err)
	}

	return ClientData{
		ID:           id,
		PrivateKey:   awg.KeyToBase64(privateKey),
		PublicKey:    awg.KeyToBase64(awg.PublicKeyFromPrivate(privateKey)),
		PresharedKey: awg.KeyToBase64(presharedKey),
		Address:      address,
		LANGroupID:   lanGroupID,
	}
}
