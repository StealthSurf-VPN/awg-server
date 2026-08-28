package clients

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

type managerTestPool struct {
	events          []string
	profiles        []awg.Profile
	requestedPorts  []int
	migrations      []managerMigration
	firewallCalls   [][]awg.LANPeer
	activeLANPeers  []awg.LANPeer
	firewallErrAt   int
	firewallCallNum int
	addErr          error
	removeErr       error
	migrateErr      error
	portErr         error
	ports           map[awg.ProfileKey]int
}

type managerMigration struct {
	oldProfile    awg.Profile
	newProfile    awg.Profile
	requestedPort int
}

func TestPrepareRestorePlanStagesMixedRecordsWithoutDeviceFirewallOrStorageMutation(t *testing.T) {
	cfg := restoreConfigForTest(t)
	data := &StorageData{
		AWG31: restoreAWG31Storage(),
		Clients: []ClientData{
			restoreClientData("legacy", "10.100.0.2", awg.ProtocolVersion2, ""),
			restoreClientData("modern", "10.100.0.3", awg.ProtocolVersion31, "opaque-default-id"),
		},
	}
	storage := NewStorage(t.TempDir())
	if err := storage.Save(data); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	before, err := os.ReadFile(storage.filePath)
	if err != nil {
		t.Fatalf("read storage before planning: %v", err)
	}

	pool := &managerTestPool{}
	plan, err := PrepareRestorePlan(cfg, restoreDefaultsForTest(t), data)
	if err != nil {
		t.Fatalf("PrepareRestorePlan() error = %v", err)
	}
	if len(plan.entries) != 2 {
		t.Fatalf("restore entries = %d, want 2", len(plan.entries))
	}
	if plan.entries[0].profile.Version() != awg.ProtocolVersion2 || plan.entries[1].profile.Version() != awg.ProtocolVersion31 {
		t.Fatalf("restore versions = %s, %s", plan.entries[0].profile.Version(), plan.entries[1].profile.Version())
	}
	if len(pool.events) != 0 || len(pool.firewallCalls) != 0 {
		t.Fatalf("pure restore plan mutated pool: events=%v firewall=%v", pool.events, pool.firewallCalls)
	}
	after, err := os.ReadFile(storage.filePath)
	if err != nil {
		t.Fatalf("read storage after planning: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("PrepareRestorePlan() changed persisted storage")
	}
}

func TestPrepareRestorePlanRejectsLaterInvalidRecordBeforeAnyRestoreMutation(t *testing.T) {
	cfg := restoreConfigForTest(t)
	data := &StorageData{
		AWG31: restoreAWG31Storage(),
		Clients: []ClientData{
			restoreClientData("valid", "10.100.0.2", awg.ProtocolVersion2, ""),
			restoreClientData("invalid", "10.100.0.3", awg.ProtocolVersion31, "missing-key-id"),
		},
	}
	pool := &managerTestPool{}

	_, err := PrepareRestorePlan(cfg, restoreDefaultsForTest(t), data)
	if err == nil || strings.Contains(err.Error(), "missing-key-id") {
		t.Fatalf("PrepareRestorePlan() error = %v, want redacted missing-key failure", err)
	}
	if len(pool.events) != 0 || len(pool.firewallCalls) != 0 {
		t.Fatalf("invalid planning mutated pool: events=%v firewall=%v", pool.events, pool.firewallCalls)
	}
	if data.AWG31.HeaderKeys["opaque-default-id"].HeaderProtectionKey != syntheticStorageHeaderKey() {
		t.Fatal("invalid planning changed persisted header key state")
	}
}

func TestPrepareRestorePlanStagesLegacyNormalizationAndPendingAWG31Defaults(t *testing.T) {
	cfg := restoreConfigForTest(t)
	data := &StorageData{Clients: []ClientData{
		restoreClientData("legacy", "10.100.0.2", "", ""),
	}}

	plan, err := PrepareRestorePlan(cfg, restoreDefaultsForTest(t), data)
	if err != nil {
		t.Fatalf("PrepareRestorePlan() error = %v", err)
	}
	if data.AWG31 != nil || data.Clients[0].ProtocolVersion != "" || data.Clients[0].LANGroupID != "" {
		t.Fatalf("source data mutated during planning: %+v", data)
	}
	if !plan.needsNormalization || plan.data.AWG31 == nil || plan.data.AWG31.GeneratedParams == nil {
		t.Fatalf("pending AWG31 state = %+v, want staged generated state", plan.data.AWG31)
	}
	if got := plan.data.Clients[0].ProtocolVersion; got != awg.ProtocolVersion2 {
		t.Fatalf("normalized legacy version = %q, want 2.0", got)
	}
	if got := plan.data.Clients[0].LANGroupID; got != "peer:legacy" {
		t.Fatalf("normalized legacy LAN group = %q, want peer:legacy", got)
	}
}

func TestPrepareRestorePlanMarksNormalizedOverridesForOneDeferredSave(t *testing.T) {
	cfg := restoreConfigForTest(t)
	client := restoreClientData("normalized", "10.100.0.2", awg.ProtocolVersion2, "")
	client.LANGroupID = "peer:normalized"
	client.AWGParams = &awg.AWGParams{
		DNSMode:    awg.DNSModeCustom,
		DNSServers: []string{"9.9.9.9", "9.9.9.9"},
	}
	client.Routing = &Routing{Mode: RoutingModeFull}
	data := &StorageData{
		AWG31:   restoreAWG31Storage(),
		Clients: []ClientData{client},
	}

	plan, err := PrepareRestorePlan(cfg, restoreDefaultsForTest(t), data)
	if err != nil {
		t.Fatalf("PrepareRestorePlan() error = %v", err)
	}
	if !plan.needsNormalization {
		t.Fatal("PrepareRestorePlan() did not mark normalized overrides for persistence")
	}
	if got := plan.data.Clients[0].AWGParams.DNSServers; len(got) != 1 || got[0] != "9.9.9.9" {
		t.Fatalf("normalized DNS servers = %v, want one canonical entry", got)
	}
	if plan.data.Clients[0].Routing != nil {
		t.Fatalf("normalized full routing = %+v, want nil", plan.data.Clients[0].Routing)
	}
	if len(data.Clients[0].AWGParams.DNSServers) != 2 || data.Clients[0].Routing == nil {
		t.Fatalf("source data mutated during normalization: %+v", data.Clients[0])
	}
}

func TestPrepareRestorePlanRejectsInterfaceConflictsAndLimitsBeforePoolConstruction(t *testing.T) {
	t.Run("same profile with different explicit ports", func(t *testing.T) {
		cfg := restoreConfigForTest(t)
		first := restoreClientData("first", "10.100.0.2", awg.ProtocolVersion2, "")
		first.AWGParams = &awg.AWGParams{Port: 51820}
		second := restoreClientData("second", "10.100.0.3", awg.ProtocolVersion2, "")
		second.AWGParams = &awg.AWGParams{Port: 51821}

		_, err := PrepareRestorePlan(cfg, restoreDefaultsForTest(t), &StorageData{Clients: []ClientData{first, second}})
		if !errors.Is(err, awg.ErrProfilePortConflict) {
			t.Fatalf("PrepareRestorePlan() error = %v, want ErrProfilePortConflict", err)
		}
	})

	t.Run("maximum interfaces", func(t *testing.T) {
		cfg := restoreConfigForTest(t)
		cfg.MaxInterfaces = 1
		data := &StorageData{
			AWG31: restoreAWG31Storage(),
			Clients: []ClientData{
				restoreClientData("legacy", "10.100.0.2", awg.ProtocolVersion2, ""),
				restoreClientData("modern", "10.100.0.3", awg.ProtocolVersion31, "opaque-default-id"),
			},
		}

		_, err := PrepareRestorePlan(cfg, restoreDefaultsForTest(t), data)
		if !errors.Is(err, awg.ErrMaxInterfacesReached) {
			t.Fatalf("PrepareRestorePlan() error = %v, want ErrMaxInterfacesReached", err)
		}
	})
}

func TestPrepareRestorePlanRejectsDuplicatePeerKeyBeforePoolMutation(t *testing.T) {
	cfg := restoreConfigForTest(t)
	first := restoreClientData("first", "10.100.0.2", awg.ProtocolVersion2, "")
	second := first
	second.ID = "second"
	second.Address = "10.100.0.3"

	_, err := PrepareRestorePlan(cfg, restoreDefaultsForTest(t), &StorageData{Clients: []ClientData{first, second}})
	if err == nil || !strings.Contains(err.Error(), "duplicate peer key") {
		t.Fatalf("PrepareRestorePlan() error = %v, want duplicate peer key", err)
	}
}

func TestPrepareRestorePlanRejectsIncompleteAWG31StateWithoutReplacement(t *testing.T) {
	tests := []struct {
		name string
		data *StorageData
	}{
		{
			name: "missing top-level state",
			data: &StorageData{Clients: []ClientData{
				restoreClientData("modern", "10.100.0.2", awg.ProtocolVersion31, "opaque-default-id"),
			}},
		},
		{
			name: "missing client reference",
			data: &StorageData{
				AWG31: restoreAWG31Storage(),
				Clients: []ClientData{
					restoreClientData("modern", "10.100.0.2", awg.ProtocolVersion31, "missing-key-id"),
				},
			},
		},
		{
			name: "malformed persisted key",
			data: func() *StorageData {
				storage := restoreAWG31Storage()
				storage.HeaderKeys["opaque-default-id"] = HeaderKeyData{HeaderProtectionKey: "synthetic-not-base64"}

				return &StorageData{
					AWG31: storage,
					Clients: []ClientData{
						restoreClientData("modern", "10.100.0.2", awg.ProtocolVersion31, "opaque-default-id"),
					},
				}
			}(),
		},
		{
			name: "zero persisted key",
			data: func() *StorageData {
				storage := restoreAWG31Storage()
				storage.HeaderKeys["opaque-default-id"] = HeaderKeyData{HeaderProtectionKey: zeroStorageHeaderKey()}

				return &StorageData{
					AWG31: storage,
					Clients: []ClientData{
						restoreClientData("modern", "10.100.0.2", awg.ProtocolVersion31, "opaque-default-id"),
					},
				}
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PrepareRestorePlan(restoreConfigForTest(t), restoreDefaultsForTest(t), tt.data)
			if err == nil || strings.Contains(err.Error(), "missing-key-id") || strings.Contains(err.Error(), "synthetic-not-base64") {
				t.Fatalf("PrepareRestorePlan() error = %v, want redacted failure", err)
			}
			if tt.data.AWG31 == nil {
				return
			}
			if tt.data.AWG31.HeaderKeys["opaque-default-id"].HeaderProtectionKey == "" {
				t.Fatal("failed restore replaced AWG 3.1 key state")
			}
		})
	}
}

func TestNewManagerFromRestorePlanRestoresThenPersistsNormalization(t *testing.T) {
	cfg := restoreConfigForTest(t)
	data := &StorageData{Clients: []ClientData{
		restoreClientData("legacy", "10.100.0.2", "", ""),
	}}
	storage := NewStorage(t.TempDir())
	if err := storage.Save(data); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	plan, err := PrepareRestorePlan(cfg, restoreDefaultsForTest(t), data)
	if err != nil {
		t.Fatalf("PrepareRestorePlan() error = %v", err)
	}
	pool := &managerTestPool{}
	manager, err := NewManagerFromRestorePlan(pool, storage, cfg, plan)
	if err != nil {
		t.Fatalf("NewManagerFromRestorePlan() error = %v", err)
	}
	if len(pool.profiles) != 1 || pool.profiles[0].Version() != awg.ProtocolVersion2 {
		t.Fatalf("restored pool profiles = %+v", pool.profiles)
	}
	if len(pool.activeLANPeers) != 1 {
		t.Fatalf("restored firewall peers = %+v", pool.activeLANPeers)
	}

	client, err := manager.GetClient("legacy")
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if client.ProtocolVersion != awg.ProtocolVersion2 || client.LANGroupID != "peer:legacy" {
		t.Fatalf("restored client = %+v", client)
	}
	stored, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AWG31 == nil || stored.Clients[0].ProtocolVersion != awg.ProtocolVersion2 {
		t.Fatalf("normalization was not persisted: %+v", stored)
	}
}

func TestCreateClientDefaultsToAWG31AndSupportsExplicitLegacyVersion(t *testing.T) {
	manager, pool, storage := newMixedManagerTest(t, &StorageData{AWG31: restoreAWG31Storage()})
	pool.reset()

	modern, err := manager.CreateClient("modern", nil, nil, "")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if modern.ProtocolVersion != awg.ProtocolVersion31 || modern.headerKeyID != "opaque-default-id" {
		t.Fatalf("default client = %+v", modern)
	}
	if len(pool.profiles) != 1 || pool.profiles[0].Version() != awg.ProtocolVersion31 {
		t.Fatalf("default pool profile = %+v", pool.profiles)
	}

	legacy, err := manager.CreateClientWithVersion("legacy", awg.ProtocolVersion2, nil, nil, "")
	if err != nil {
		t.Fatalf("CreateClientWithVersion() error = %v", err)
	}
	if legacy.ProtocolVersion != awg.ProtocolVersion2 || legacy.headerKeyID != "" {
		t.Fatalf("explicit legacy client = %+v", legacy)
	}
	if len(pool.profiles) != 2 || pool.profiles[1].Version() != awg.ProtocolVersion2 {
		t.Fatalf("explicit legacy profile = %+v", pool.profiles)
	}

	stored, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.Clients[0].ProtocolVersion != awg.ProtocolVersion31 || stored.Clients[1].ProtocolVersion != awg.ProtocolVersion2 {
		t.Fatalf("persisted versions = %+v", stored.Clients)
	}

	configuration, err := manager.GetClientConfig("modern")
	if err != nil {
		t.Fatalf("GetClientConfig(modern) error = %v", err)
	}
	if !strings.Contains(configuration, "HeaderProtectionKey = "+syntheticStorageHeaderKey()) ||
		!strings.Contains(configuration, "ContentPaddingAddition = 10-100") ||
		strings.Contains(configuration, "ProtocolVersion") {
		t.Fatalf("AWG 3.1 configuration =\n%s", configuration)
	}

	legacyConfiguration, err := manager.GetClientConfig("legacy")
	if err != nil {
		t.Fatalf("GetClientConfig(legacy) error = %v", err)
	}
	if strings.Contains(legacyConfiguration, "HeaderProtectionKey") || strings.Contains(legacyConfiguration, "ContentPaddingAddition") {
		t.Fatalf("legacy configuration contains AWG 3.1 fields:\n%s", legacyConfiguration)
	}
}

func TestAWG31PatchResetAndRegenerationManagePrivateKeyReferencesTransactionally(t *testing.T) {
	manager, _, _ := newMixedManagerTest(t, &StorageData{AWG31: restoreAWG31Storage()})

	created, err := manager.CreateClient("modern", nil, nil, "")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if created.headerKeyID != "opaque-default-id" {
		t.Fatalf("created key ID = %q, want default", created.headerKeyID)
	}

	regenerated, err := manager.RegenerateAWGParams("modern", passthroughMigrationGuard)
	if err != nil {
		t.Fatalf("RegenerateAWGParams() error = %v", err)
	}
	if regenerated.headerKeyID == "opaque-default-id" {
		t.Fatal("AWG 3.1 regeneration did not rotate the header key reference")
	}
	rotatedKeyID := regenerated.headerKeyID
	if _, exists := manager.data.AWG31.HeaderKeys[rotatedKeyID]; !exists {
		t.Fatal("rotated header key was not staged and committed")
	}

	patched, err := manager.UpdateClient("modern", ClientUpdate{
		Routing:    &Routing{Mode: RoutingModeSplit, AllowedIPs: []string{"198.51.100.0/24"}},
		RoutingSet: true,
	}, nil)
	if err != nil {
		t.Fatalf("UpdateClient() error = %v", err)
	}
	if patched.headerKeyID != rotatedKeyID {
		t.Fatalf("ordinary PATCH changed key reference from %q to %q", rotatedKeyID, patched.headerKeyID)
	}

	reset, err := manager.UpdateClient("modern", ClientUpdate{AWGParamsSet: true}, passthroughMigrationGuard)
	if err != nil {
		t.Fatalf("UpdateClient(reset) error = %v", err)
	}
	if reset.headerKeyID != "opaque-default-id" || reset.AWGParams != nil {
		t.Fatalf("reset client = %+v", reset)
	}
	if _, exists := manager.data.AWG31.HeaderKeys[rotatedKeyID]; exists {
		t.Fatal("unreferenced rotated key remained after reset")
	}
	if _, exists := manager.data.AWG31.HeaderKeys["opaque-default-id"]; !exists {
		t.Fatal("default header key was removed during mark-and-sweep")
	}
}

func TestVersionMigrationRequiresSnapshotBeforeKeyPoolOrStorageMutation(t *testing.T) {
	manager, pool, _ := newMixedManagerTest(t, &StorageData{AWG31: restoreAWG31Storage()})
	legacy, err := manager.CreateClientWithVersion("legacy", awg.ProtocolVersion2, nil, &Routing{
		Mode: RoutingModeSplit, AllowedIPs: []string{"198.51.100.0/24"},
	}, "")
	if err != nil {
		t.Fatalf("CreateClientWithVersion() error = %v", err)
	}
	pool.reset()

	snapshotErr := errors.New("usage snapshot unavailable")
	_, err = manager.UpdateClient("legacy", ClientUpdate{
		ProtocolVersion:    awg.ProtocolVersion31,
		ProtocolVersionSet: true,
	}, func(func() error) error {
		return snapshotErr
	})
	if !errors.Is(err, snapshotErr) {
		t.Fatalf("UpdateClient() error = %v, want snapshot failure", err)
	}
	if len(pool.migrations) != 0 || len(pool.profiles) != 0 {
		t.Fatalf("snapshot failure mutated pool: migrations=%+v profiles=%+v", pool.migrations, pool.profiles)
	}
	unchanged, err := manager.GetClient("legacy")
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if unchanged.ProtocolVersion != awg.ProtocolVersion2 || unchanged.headerKeyID != "" || unchanged.Address != legacy.Address {
		t.Fatalf("snapshot failure changed client: %+v", unchanged)
	}
	if len(manager.data.AWG31.HeaderKeys) != 1 {
		t.Fatalf("snapshot failure changed header key state: %+v", manager.data.AWG31.HeaderKeys)
	}

	migrated, err := manager.UpdateClient("legacy", ClientUpdate{
		ProtocolVersion:    awg.ProtocolVersion31,
		ProtocolVersionSet: true,
	}, passthroughMigrationGuard)
	if err != nil {
		t.Fatalf("UpdateClient(migrate) error = %v", err)
	}
	if migrated.ProtocolVersion != awg.ProtocolVersion31 || migrated.headerKeyID != "opaque-default-id" ||
		migrated.Address != legacy.Address || migrated.PrivateKey != legacy.PrivateKey || migrated.PublicKey != legacy.PublicKey ||
		migrated.Routing.Mode != RoutingModeSplit {
		t.Fatalf("migrated client = %+v", migrated)
	}
	if len(pool.migrations) != 1 || pool.migrations[0].oldProfile.Version() != awg.ProtocolVersion2 || pool.migrations[0].newProfile.Version() != awg.ProtocolVersion31 {
		t.Fatalf("migration calls = %+v", pool.migrations)
	}
}

func TestDowngradeRejectsIncompatibleOverridesBeforeMutationAndAllowsExplicitReset(t *testing.T) {
	manager, pool, _ := newMixedManagerTest(t, &StorageData{AWG31: restoreAWG31Storage()})
	created, err := manager.CreateClient("modern", &awg.AWGParams{
		PersistentKeepalive: managerRangePointer(t, "25-35"),
	}, nil, "")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	pool.reset()

	_, err = manager.UpdateClient("modern", ClientUpdate{
		ProtocolVersion:    awg.ProtocolVersion2,
		ProtocolVersionSet: true,
	}, func(func() error) error {
		t.Fatal("incompatible downgrade reached snapshot guard")

		return nil
	})
	if !errors.Is(err, awg.ErrInvalidParams) {
		t.Fatalf("UpdateClient() error = %v, want invalid params", err)
	}
	if len(pool.migrations) != 0 {
		t.Fatalf("incompatible downgrade mutated pool: %+v", pool.migrations)
	}
	unchanged, _ := manager.GetClient("modern")
	if unchanged.ProtocolVersion != awg.ProtocolVersion31 || unchanged.headerKeyID != created.headerKeyID {
		t.Fatalf("incompatible downgrade changed client: %+v", unchanged)
	}

	downgraded, err := manager.UpdateClient("modern", ClientUpdate{
		ProtocolVersion:    awg.ProtocolVersion2,
		ProtocolVersionSet: true,
		AWGParamsSet:       true,
	}, passthroughMigrationGuard)
	if err != nil {
		t.Fatalf("UpdateClient(reset and downgrade) error = %v", err)
	}
	if downgraded.ProtocolVersion != awg.ProtocolVersion2 || downgraded.headerKeyID != "" || downgraded.AWGParams != nil {
		t.Fatalf("downgraded client = %+v", downgraded)
	}
}

func TestDowngradeSaveFailureRollsBackExactProfilePortAndHeaderState(t *testing.T) {
	manager, pool, storage := newMixedManagerTest(t, &StorageData{AWG31: restoreAWG31Storage()})
	if _, err := manager.CreateClient("modern", nil, nil, ""); err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if _, err := manager.RegenerateAWGParams("modern", passthroughMigrationGuard); err != nil {
		t.Fatalf("RegenerateAWGParams() error = %v", err)
	}
	before, err := manager.GetClient("modern")
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	oldProfile, err := effectiveProfileForData(manager.defaults, manager.data, before.ProtocolVersion, before.AWGParams, before.headerKeyID)
	if err != nil {
		t.Fatalf("effectiveProfileForData() error = %v", err)
	}
	pool.reset()
	pool.ports = map[awg.ProfileKey]int{oldProfile.Key(): 51999}
	storage.filePath = t.TempDir()

	_, err = manager.UpdateClient("modern", ClientUpdate{
		ProtocolVersion:    awg.ProtocolVersion2,
		ProtocolVersionSet: true,
		AWGParamsSet:       true,
	}, passthroughMigrationGuard)
	if err == nil || !strings.Contains(err.Error(), "save client update") {
		t.Fatalf("UpdateClient() error = %v, want save failure", err)
	}
	if len(pool.migrations) != 2 {
		t.Fatalf("migration calls = %+v, want forward and exact rollback", pool.migrations)
	}
	rollback := pool.migrations[1]
	if rollback.newProfile.Key() != oldProfile.Key() || rollback.requestedPort != 51999 {
		t.Fatalf("rollback = %+v, want old profile on actual port 51999", rollback)
	}
	after, err := manager.GetClient("modern")
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if after.ProtocolVersion != before.ProtocolVersion || after.headerKeyID != before.headerKeyID {
		t.Fatalf("save failure changed client: before=%+v after=%+v", before, after)
	}
	if _, exists := manager.data.AWG31.HeaderKeys[before.headerKeyID]; !exists {
		t.Fatal("save failure discarded old header key state")
	}
}

func TestDeleteSaveFailureReaddsExactAWG31ProfileAndRetainsHeaderKey(t *testing.T) {
	manager, pool, storage := newMixedManagerTest(t, &StorageData{AWG31: restoreAWG31Storage()})
	if _, err := manager.CreateClient("modern", nil, nil, ""); err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if _, err := manager.RegenerateAWGParams("modern", passthroughMigrationGuard); err != nil {
		t.Fatalf("RegenerateAWGParams() error = %v", err)
	}
	before, err := manager.GetClient("modern")
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	oldProfile, err := effectiveProfileForData(manager.defaults, manager.data, before.ProtocolVersion, before.AWGParams, before.headerKeyID)
	if err != nil {
		t.Fatalf("effectiveProfileForData() error = %v", err)
	}
	pool.reset()
	storage.filePath = t.TempDir()

	err = manager.DeleteClient("modern")
	if err == nil || !strings.Contains(err.Error(), "save deleted client") {
		t.Fatalf("DeleteClient() error = %v, want save failure", err)
	}
	if len(pool.profiles) < 2 || pool.profiles[0].Key() != oldProfile.Key() || pool.profiles[1].Key() != oldProfile.Key() {
		t.Fatalf("delete rollback profiles = %+v", pool.profiles)
	}
	after, err := manager.GetClient("modern")
	if err != nil {
		t.Fatalf("GetClient() after failed delete error = %v", err)
	}
	if after.headerKeyID != before.headerKeyID {
		t.Fatalf("delete save failure changed header key ref from %q to %q", before.headerKeyID, after.headerKeyID)
	}
	if _, exists := manager.data.AWG31.HeaderKeys[before.headerKeyID]; !exists {
		t.Fatal("delete save failure discarded header key")
	}
}

func (p *managerTestPool) AddPeer(profile awg.Profile, requestedPort int, _ [32]byte, _ *[32]byte, address string) error {
	p.events = append(p.events, "add:"+address)
	p.profiles = append(p.profiles, profile)
	p.requestedPorts = append(p.requestedPorts, requestedPort)
	return p.addErr
}

func (p *managerTestPool) RemovePeer(profile awg.Profile, _ [32]byte, address string) error {
	p.events = append(p.events, "remove:"+address)
	p.profiles = append(p.profiles, profile)
	return p.removeErr
}

func (p *managerTestPool) MigratePeer(oldProfile, newProfile awg.Profile, requestedPort int, _ [32]byte, _ *[32]byte, _ string) error {
	p.events = append(p.events, "migrate")
	p.migrations = append(p.migrations, managerMigration{
		oldProfile: oldProfile, newProfile: newProfile, requestedPort: requestedPort,
	})
	p.profiles = append(p.profiles, oldProfile, newProfile)
	p.requestedPorts = append(p.requestedPorts, requestedPort)

	return p.migrateErr
}

func (p *managerTestPool) PortForProfile(profile awg.Profile) (int, error) {
	if p.portErr != nil {
		return 0, p.portErr
	}
	if p.ports != nil {
		if port, exists := p.ports[profile.Key()]; exists {
			return port, nil
		}
	}

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
	p.profiles = nil
	p.requestedPorts = nil
	p.migrations = nil
	p.firewallCalls = nil
	p.firewallCallNum = 0
	p.firewallErrAt = 0
	p.addErr = nil
	p.removeErr = nil
	p.migrateErr = nil
	p.portErr = nil
}

func TestManagerBuildsValidatedLegacyProfiles(t *testing.T) {
	manager, _, _ := newManagerTest(t, &StorageData{})

	profile, err := manager.effectiveProfile(&awg.AWGParams{
		Port:             51830,
		ClientListenPort: 51831,
		MTU:              1380,
		I1:               "<t>",
	})
	if err != nil {
		t.Fatalf("effectiveProfile() error = %v", err)
	}
	if profile.Version() != awg.ProtocolVersion2 {
		t.Fatalf("profile version = %s, want 2.0", profile.Version())
	}

	params := profile.Params()
	if params.Port != 51830 || params.ClientListenPort != 51831 || params.MTU != 1380 || params.I1 != "<t>" {
		t.Fatalf("profile params = %+v", params)
	}

	defaultProfile, err := manager.effectiveProfile(nil)
	if err != nil {
		t.Fatalf("effectiveProfile(nil) error = %v", err)
	}
	if profile.Key() != defaultProfile.Key() {
		t.Fatal("legacy ProfileKey changed for client-only fields or requested port")
	}
}

func TestCreateClientPassesLegacyProfileAndSeparateRequestedPort(t *testing.T) {
	manager, pool, _ := newManagerTest(t, &StorageData{})
	pool.reset()

	if _, err := manager.CreateClient("device", &awg.AWGParams{Port: 51830}, nil, ""); err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if len(pool.profiles) != 1 || len(pool.requestedPorts) != 1 {
		t.Fatalf("pool calls = profiles:%d ports:%d", len(pool.profiles), len(pool.requestedPorts))
	}
	if pool.profiles[0].Version() != awg.ProtocolVersion2 {
		t.Fatalf("profile version = %s, want 2.0", pool.profiles[0].Version())
	}
	if pool.requestedPorts[0] != 51830 {
		t.Fatalf("requested port = %d, want 51830", pool.requestedPorts[0])
	}
}

func TestCreateClientModeBasedDNSReplacesInheritedLegacyDNS(t *testing.T) {
	manager, _, _ := newManagerTest(t, &StorageData{})

	client, err := manager.CreateClient("custom-dns", &awg.AWGParams{
		DNSMode:    awg.DNSModeCustom,
		DNSServers: []string{"9.9.9.9"},
	}, nil, "")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	configuration, err := manager.GetClientConfig(client.ID)
	if err != nil {
		t.Fatalf("GetClientConfig() error = %v", err)
	}
	if !strings.Contains(configuration, "DNS = 9.9.9.9") {
		t.Fatalf("configuration does not use custom DNS:\n%s", configuration)
	}
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

func TestRenderClientConfigUsesCanonicalPersistentKeepalive(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "legacy default", want: "25"},
		{name: "scalar zero", value: "0", want: "0"},
		{name: "range", value: "25-35", want: "25-35"},
		{name: "off", value: "off", want: "off"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := awg.AWGParams{MTU: 1420}
			if tt.value != "" {
				params.PersistentKeepalive = managerRangePointer(t, tt.value)
			}
			client := &ClientData{PrivateKey: "private", Address: "10.100.0.2"}

			configuration, err := renderClientConfig(client, params, [32]byte{}, "10.100.0.0/24", "vpn.example.test", 51820)
			if err != nil {
				t.Fatalf("renderClientConfig() error = %v", err)
			}
			if !strings.Contains(configuration, "PersistentKeepalive = "+tt.want) {
				t.Fatalf("configuration missing canonical keepalive %q:\n%s", tt.want, configuration)
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
	defaults := restoreDefaultsForTest(t)
	defaults.LegacyParams = params
	defaults.DefaultVersion = awg.ProtocolVersion2
	plan, err := PrepareRestorePlan(cfg, defaults, data)
	if err != nil {
		t.Fatalf("PrepareRestorePlan() error = %v", err)
	}
	manager, err := NewManagerFromRestorePlan(pool, storage, cfg, plan)
	if err != nil {
		t.Fatalf("NewManagerFromRestorePlan() error = %v", err)
	}

	return manager, pool, storage
}

func validStoredClient(t *testing.T, id, address, lanGroupID string) ClientData {
	t.Helper()

	seed := syntheticManagerKeySeed(id)
	privateKey := syntheticManagerKey(seed)
	presharedKey := syntheticManagerKey(seed + 67)

	return ClientData{
		ID:           id,
		PrivateKey:   awg.KeyToBase64(privateKey),
		PublicKey:    awg.KeyToBase64(awg.PublicKeyFromPrivate(privateKey)),
		PresharedKey: awg.KeyToBase64(presharedKey),
		Address:      address,
		LANGroupID:   lanGroupID,
	}
}

func managerRangePointer(t *testing.T, value string) *config.Uint16Range {
	t.Helper()

	parsed, err := config.ParseUint16Range(value)
	if err != nil {
		t.Fatalf("ParseUint16Range(%q) error = %v", value, err)
	}

	return &parsed
}

func restoreConfigForTest(t *testing.T) *config.Config {
	t.Helper()

	return &config.Config{
		Address:       "10.100.0.1/24",
		Endpoint:      "vpn.example.test",
		ListenPort:    51820,
		MTU:           1420,
		DNS:           "1.1.1.1",
		DataDir:       t.TempDir(),
		MaxInterfaces: 0,
	}
}

func restoreDefaultsForTest(t *testing.T) ManagerDefaults {
	t.Helper()

	return ManagerDefaults{
		LegacyParams: awg.AWGParams{
			MTU: 1420, DNS: "1.1.1.1",
			Jc: 5, Jmin: 50, Jmax: 1000,
			S1: 15, S2: 72,
			H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
		},
		AWG31Params: awg.AWGParams{
			MTU: 1280, DNS: "1.1.1.1",
			Jc: 5, Jmin: 50, Jmax: 1000,
			PersistentKeepalive:    managerRangePointer(t, "25-35"),
			ContentPaddingAddition: managerRangePointer(t, "10-100"),
			RekeyAfterTime:         managerRangePointer(t, "100-120"),
			RekeyTimeout:           managerRangePointer(t, "3-7"),
			RejectAfterTime:        managerRangePointer(t, "150-180"),
			KeepaliveTimeout:       managerRangePointer(t, "5-15"),
			MaxHandshakeAttempts:   managerRangePointer(t, "15-20"),
			RandomTrailers:         "on",
			DisableCookies:         "off",
		},
		DefaultVersion: awg.ProtocolVersion31,
	}
}

func restoreAWG31Storage() *AWG31Storage {
	return &AWG31Storage{
		DefaultHeaderKeyID: "opaque-default-id",
		GeneratedParams: &awg.GeneratedParamsV31{
			H1: "100001", H2: "1000001", H3: "10000001", H4: "100000001",
			S1: 15, S2: 72, S3: 15, S4: 12,
		},
		HeaderKeys: map[string]HeaderKeyData{
			"opaque-default-id": {HeaderProtectionKey: syntheticStorageHeaderKey()},
		},
	}
}

func restoreClientData(id, address string, version awg.ProtocolVersion, headerKeyID string) ClientData {
	seed := syntheticManagerKeySeed(id)
	privateKey := syntheticManagerKey(seed)
	presharedKey := syntheticManagerKey(seed + 67)

	return ClientData{
		ID:              id,
		ProtocolVersion: version,
		PrivateKey:      awg.KeyToBase64(privateKey),
		PublicKey:       awg.KeyToBase64(awg.PublicKeyFromPrivate(privateKey)),
		PresharedKey:    awg.KeyToBase64(presharedKey),
		Address:         address,
		LANGroupID:      "",
		CreatedAt:       "2026-08-28T00:00:00Z",
		headerKeyID:     headerKeyID,
	}
}

func syntheticManagerKeySeed(id string) byte {
	seed := byte(1)
	for index := range id {
		seed += id[index]
	}

	return seed
}

func syntheticManagerKey(seed byte) [32]byte {
	var key [32]byte
	for index := range key {
		key[index] = seed + byte(index)
	}

	return key
}

func zeroStorageHeaderKey() string {
	return base64.StdEncoding.EncodeToString(make([]byte, 32))
}

func newMixedManagerTest(t *testing.T, data *StorageData) (*Manager, *managerTestPool, *Storage) {
	t.Helper()

	cfg := restoreConfigForTest(t)
	storage := NewStorage(cfg.DataDir)
	if err := storage.Save(data); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	plan, err := PrepareRestorePlan(cfg, restoreDefaultsForTest(t), data)
	if err != nil {
		t.Fatalf("PrepareRestorePlan() error = %v", err)
	}
	pool := &managerTestPool{}
	manager, err := NewManagerFromRestorePlan(pool, storage, cfg, plan)
	if err != nil {
		t.Fatalf("NewManagerFromRestorePlan() error = %v", err)
	}

	return manager, pool, storage
}

func passthroughMigrationGuard(transaction func() error) error {
	return transaction()
}
