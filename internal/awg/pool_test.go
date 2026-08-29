package awg

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

func TestPoolSharesOneInterfaceForSameProfile(t *testing.T) {
	operations := &poolTestOperations{}
	pool := newPoolTestSubject(operations)
	profile := mustLegacyProfile(t, validLegacyProfileParams())

	if err := pool.AddPeer(profile, 0, poolTestKey(1), nil, "10.77.0.2"); err != nil {
		t.Fatalf("AddPeer(first) error = %v", err)
	}
	if err := pool.AddPeer(profile, 51820, poolTestKey(2), nil, "10.77.0.3"); err != nil {
		t.Fatalf("AddPeer(second) error = %v", err)
	}

	if len(pool.ifaces) != 1 {
		t.Fatalf("interface count = %d, want 1", len(pool.ifaces))
	}
	if len(operations.configurePorts) != 1 || operations.configurePorts[0] != 51820 {
		t.Fatalf("configured ports = %v, want [51820]", operations.configurePorts)
	}
	if operations.addCalls != 2 {
		t.Fatalf("peer add calls = %d, want 2", operations.addCalls)
	}
}

func TestPoolSharesOneInterfaceForEquivalentAWG31RangeSyntax(t *testing.T) {
	operations := &poolTestOperations{}
	pool := newPoolTestSubject(operations)
	pool.cfg.MaxInterfaces = 1

	firstParams := validAWG31ProfileParams(t)
	firstParams.ContentPaddingAddition = rangePointer(t, "25")
	secondParams := *cloneAWGParams(&firstParams)
	secondParams.ContentPaddingAddition = rangePointer(t, "25-25")
	headerKey := syntheticHeaderProtectionKey()

	first, err := NewAWG31Profile(firstParams, headerKey)
	if err != nil {
		t.Fatalf("NewAWG31Profile(first) error = %v", err)
	}
	second, err := NewAWG31Profile(secondParams, headerKey)
	if err != nil {
		t.Fatalf("NewAWG31Profile(second) error = %v", err)
	}

	if err := pool.AddPeer(first, 0, poolTestKey(1), nil, "10.77.0.2"); err != nil {
		t.Fatalf("AddPeer(first) error = %v", err)
	}
	if err := pool.AddPeer(second, 0, poolTestKey(2), nil, "10.77.0.3"); err != nil {
		t.Fatalf("AddPeer(second) error = %v", err)
	}

	if len(pool.ifaces) != 1 {
		t.Fatalf("interface count = %d, want 1", len(pool.ifaces))
	}
	if operations.addCalls != 2 {
		t.Fatalf("peer add calls = %d, want 2", operations.addCalls)
	}
}

func TestPoolSeparatesDifferentProfileKeys(t *testing.T) {
	operations := &poolTestOperations{}
	pool := newPoolTestSubject(operations)
	first := mustLegacyProfile(t, validLegacyProfileParams())

	secondParams := validLegacyProfileParams()
	secondParams.H1 = "9-10"
	second := mustLegacyProfile(t, secondParams)

	if err := pool.AddPeer(first, 0, poolTestKey(1), nil, "10.77.0.2"); err != nil {
		t.Fatalf("AddPeer(first) error = %v", err)
	}
	if err := pool.AddPeer(second, 0, poolTestKey(2), nil, "10.77.0.3"); err != nil {
		t.Fatalf("AddPeer(second) error = %v", err)
	}

	if len(pool.ifaces) != 2 {
		t.Fatalf("interface count = %d, want 2", len(pool.ifaces))
	}
	if got, want := fmt.Sprint(operations.configurePorts), "[51820 51821]"; got != want {
		t.Fatalf("configured ports = %s, want %s", got, want)
	}
	if operations.configureProfiles[0].Key() == operations.configureProfiles[1].Key() {
		t.Fatal("different profiles were configured with the same profile key")
	}
}

func TestPoolRejectsConflictingRequestedPortBeforePeerMutation(t *testing.T) {
	operations := &poolTestOperations{}
	pool := newPoolTestSubject(operations)
	profile := mustLegacyProfile(t, validLegacyProfileParams())

	if err := pool.AddPeer(profile, 51820, poolTestKey(1), nil, "10.77.0.2"); err != nil {
		t.Fatalf("AddPeer() error = %v", err)
	}

	peerAdds := operations.addCalls
	err := pool.AddPeer(profile, 51821, poolTestKey(2), nil, "10.77.0.3")
	if !errors.Is(err, ErrProfilePortConflict) {
		t.Fatalf("AddPeer() error = %v, want ErrProfilePortConflict", err)
	}
	if operations.addCalls != peerAdds {
		t.Fatalf("peer add calls = %d, want unchanged %d", operations.addCalls, peerAdds)
	}
}

func TestPoolCleansUpNewInterfaceAfterFailedPeerAdd(t *testing.T) {
	operations := &poolTestOperations{addErrAt: 1}
	pool := newPoolTestSubject(operations)
	profile := mustLegacyProfile(t, validLegacyProfileParams())

	err := pool.AddPeer(profile, 51820, poolTestKey(1), nil, "10.77.0.2")
	if err == nil || !strings.Contains(err.Error(), "synthetic peer add failure") {
		t.Fatalf("AddPeer() error = %v, want peer add failure", err)
	}
	if len(pool.ifaces) != 0 {
		t.Fatalf("interface count = %d, want 0 after cleanup", len(pool.ifaces))
	}
	if pool.usedPorts[51820] {
		t.Fatal("failed interface port remained reserved after successful cleanup")
	}
	if got, want := operations.events, []string{"create:awg0", "configure:awg0:51820", "network:awg0", "masquerade", "add:awg0", "cleanup:awg0", "destroy:awg0"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestPoolMigrationRollbackRestoresOldProfileAtActualRuntimePort(t *testing.T) {
	operations := &poolTestOperations{addErrAt: 2}
	pool := newPoolTestSubject(operations)
	oldProfile := mustLegacyProfile(t, validLegacyProfileParams())

	newParams := validLegacyProfileParams()
	newParams.H1 = "9-10"
	newProfile := mustLegacyProfile(t, newParams)
	publicKey := poolTestKey(1)

	if err := pool.AddPeer(oldProfile, 0, publicKey, nil, "10.77.0.2"); err != nil {
		t.Fatalf("AddPeer(old) error = %v", err)
	}
	oldPort, err := pool.PortForProfile(oldProfile)
	if err != nil {
		t.Fatalf("PortForProfile(old) before migration error = %v", err)
	}
	if oldPort != 51820 {
		t.Fatalf("old runtime port = %d, want 51820", oldPort)
	}

	err = pool.MigratePeer(oldProfile, newProfile, 51830, publicKey, nil, "10.77.0.2")
	if err == nil || !strings.Contains(err.Error(), "synthetic peer add failure") {
		t.Fatalf("MigratePeer() error = %v, want failed new peer add", err)
	}

	port, err := pool.PortForProfile(oldProfile)
	if err != nil {
		t.Fatalf("PortForProfile(old) error = %v", err)
	}
	if port != oldPort {
		t.Fatalf("restored old port = %d, want actual runtime port %d", port, oldPort)
	}
	if _, exists := pool.ifaces[newProfile.Key()]; exists {
		t.Fatal("new profile interface remains after rollback")
	}
	if got, want := fmt.Sprint(operations.configurePorts), "[51820 51830 51820]"; got != want {
		t.Fatalf("configured ports = %s, want rollback at exact old runtime port %s", got, want)
	}
	if operations.configureProfiles[2].Key() != oldProfile.Key() {
		t.Fatal("rollback did not restore the old profile")
	}
}

type poolTestOperations struct {
	events            []string
	configureProfiles []Profile
	configurePorts    []int
	addCalls          int
	addErrAt          int
}

func (operations *poolTestOperations) createInterface(ifName string) error {
	operations.events = append(operations.events, "create:"+ifName)
	return nil
}

func (operations *poolTestOperations) destroyInterface(ifName string) error {
	operations.events = append(operations.events, "destroy:"+ifName)
	return nil
}

func (operations *poolTestOperations) configureDevice(ifName string, port int, profile Profile, _ [32]byte) error {
	operations.events = append(operations.events, fmt.Sprintf("configure:%s:%d", ifName, port))
	operations.configureProfiles = append(operations.configureProfiles, profile)
	operations.configurePorts = append(operations.configurePorts, port)
	return nil
}

func (operations *poolTestOperations) configureInterfaceNetwork(ifName, _ string) error {
	operations.events = append(operations.events, "network:"+ifName)
	return nil
}

func (operations *poolTestOperations) addPeerToInterface(ifName string, _ [32]byte, _ *[32]byte, _ string) error {
	operations.events = append(operations.events, "add:"+ifName)
	operations.addCalls++
	if operations.addCalls == operations.addErrAt {
		return errors.New("synthetic peer add failure")
	}
	return nil
}

func (operations *poolTestOperations) removePeerFromInterface(ifName string, _ [32]byte, _ *[32]byte, _ string) error {
	operations.events = append(operations.events, "remove:"+ifName)
	return nil
}

func (operations *poolTestOperations) removePeerOnlyFromInterface(ifName string, _ [32]byte) error {
	operations.events = append(operations.events, "remove-only:"+ifName)
	return nil
}

func (operations *poolTestOperations) cleanupPeerAfterFailedAdd(ifName string, _ [32]byte, _ string) (bool, error) {
	operations.events = append(operations.events, "cleanup:"+ifName)
	return true, nil
}

func (operations *poolTestOperations) restorePeerAndRoute(ifName string, _ [32]byte, _ *[32]byte, _ string) error {
	operations.events = append(operations.events, "restore:"+ifName)
	return nil
}

func (operations *poolTestOperations) replacePeerRoute(ifName, _ string) error {
	operations.events = append(operations.events, "replace-route:"+ifName)
	return nil
}

func (operations *poolTestOperations) addMasquerade(_, _ string) error {
	operations.events = append(operations.events, "masquerade")
	return nil
}

func (operations *poolTestOperations) removeMasquerade(_, _ string) error {
	operations.events = append(operations.events, "remove-masquerade")
	return nil
}

func newPoolTestSubject(operations poolOperations) *Pool {
	cfg := &config.Config{
		Address:    "10.77.0.1/24",
		ListenPort: 51820,
	}
	privateKey := [32]byte{1}

	return &Pool{
		cfg:        cfg,
		privKey:    privateKey,
		pubKey:     PublicKeyFromPrivate(privateKey),
		outIface:   "eth0",
		ifaces:     make(map[ProfileKey]*iface),
		orphans:    make(map[string]int),
		usedPorts:  make(map[int]bool),
		operations: operations,
	}
}

func mustLegacyProfile(t *testing.T, params AWGParams) Profile {
	t.Helper()

	profile, err := NewLegacyProfile(params)
	if err != nil {
		t.Fatalf("NewLegacyProfile() error = %v", err)
	}

	return profile
}

func poolTestKey(value byte) [32]byte {
	return [32]byte{value}
}
