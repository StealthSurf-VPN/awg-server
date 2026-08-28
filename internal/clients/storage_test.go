package clients

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

func TestStorageLoadKeepsAbsentVersionDistinctFromExplicitCanonicalVersions(t *testing.T) {
	tests := []struct {
		name          string
		protocolField string
		wantVersion   awg.ProtocolVersion
		wantErr       bool
	}{
		{name: "legacy field absent", wantVersion: ""},
		{name: "explicit legacy", protocolField: `,"protocol_version":"2.0"`, wantVersion: awg.ProtocolVersion2},
		{name: "explicit AWG 3.1", protocolField: `,"protocol_version":"3.1"`, wantVersion: awg.ProtocolVersion31},
		{name: "legacy alias is rejected on disk", protocolField: `,"protocol_version":"2"`, wantErr: true},
		{name: "null is rejected on disk", protocolField: `,"protocol_version":null`, wantErr: true},
		{name: "number is rejected on disk", protocolField: `,"protocol_version":2`, wantErr: true},
		{name: "empty is rejected on disk", protocolField: `,"protocol_version":""`, wantErr: true},
		{name: "unknown is rejected on disk", protocolField: `,"protocol_version":"9.9"`, wantErr: true},
		{name: "case variant is rejected on disk", protocolField: `,"PROTOCOL_VERSION":"2"`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := NewStorage(t.TempDir())
			fixture := `{"clients":[{"id":"legacy","private_key":"","public_key":"","address":"10.10.0.2","lan_group_id":"peer:legacy","created_at":"2026-08-28T00:00:00Z"` + tt.protocolField + `}]}`
			if err := os.WriteFile(storage.filePath, []byte(fixture), 0600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			data, err := storage.Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() succeeded")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := data.Clients[0].ProtocolVersion; got != tt.wantVersion {
				t.Fatalf("ProtocolVersion = %q, want %q", got, tt.wantVersion)
			}
		})
	}
}

func TestStorageRoundTripKeepsPrivateHeaderReferenceOutOfPublicClientJSON(t *testing.T) {
	storage := NewStorage(t.TempDir())
	data := &StorageData{
		AWG31: &AWG31Storage{
			DefaultHeaderKeyID: "opaque-default-id",
			GeneratedParams: &awg.GeneratedParamsV31{
				H1: "100001", H2: "1000001", H3: "10000001", H4: "100000001",
				S1: 15, S2: 72, S3: 15, S4: 12,
			},
			HeaderKeys: map[string]HeaderKeyData{
				"opaque-default-id": {HeaderProtectionKey: syntheticStorageHeaderKey()},
			},
		},
		Clients: []ClientData{{
			ID:              "mixed-awg31",
			ProtocolVersion: awg.ProtocolVersion31,
			PrivateKey:      "synthetic-private",
			PublicKey:       "synthetic-public",
			Address:         "10.10.0.2",
			LANGroupID:      "peer:mixed-awg31",
			CreatedAt:       "2026-08-28T00:00:00Z",
			headerKeyID:     "opaque-default-id",
		}},
	}

	if err := storage.Save(data); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := storage.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.Clients[0].headerKeyID; got != "opaque-default-id" {
		t.Fatalf("stored header key ID = %q, want opaque-default-id", got)
	}

	publicJSON, err := json.Marshal(loaded.Clients[0])
	if err != nil {
		t.Fatalf("marshal public client: %v", err)
	}
	if strings.Contains(string(publicJSON), "header_key_id") || strings.Contains(string(publicJSON), "opaque-default-id") {
		t.Fatalf("public client JSON exposes private header reference: %s", publicJSON)
	}
}

func TestCloneStorageDataDoesNotAliasNestedMutableState(t *testing.T) {
	rangeValue, err := config.ParseUint16Range("25-35")
	if err != nil {
		t.Fatalf("ParseUint16Range() error = %v", err)
	}

	original := &StorageData{
		AWG31: &AWG31Storage{
			DefaultHeaderKeyID: "opaque-default-id",
			HeaderKeys: map[string]HeaderKeyData{
				"opaque-default-id": {HeaderProtectionKey: syntheticStorageHeaderKey()},
			},
		},
		Clients: []ClientData{{
			ID:              "client",
			ProtocolVersion: awg.ProtocolVersion31,
			headerKeyID:     "opaque-default-id",
			AWGParams: &awg.AWGParams{
				PersistentKeepalive: &rangeValue,
				DNSServers:          []string{"1.1.1.1"},
			},
			Routing: &Routing{Mode: RoutingModeSplit, AllowedIPs: []string{"198.51.100.0/24"}},
		}},
	}

	clone := cloneStorageData(original)
	clone.AWG31.HeaderKeys["opaque-default-id"] = HeaderKeyData{HeaderProtectionKey: "changed"}
	clone.Clients[0].AWGParams.PersistentKeepalive = mustStorageRange(t, "40-50")
	clone.Clients[0].AWGParams.DNSServers[0] = "9.9.9.9"
	clone.Clients[0].Routing.AllowedIPs[0] = "203.0.113.0/24"

	if original.AWG31.HeaderKeys["opaque-default-id"].HeaderProtectionKey == "changed" {
		t.Fatal("header key map aliases prospective copy")
	}
	if got := original.Clients[0].AWGParams.PersistentKeepalive.String(); got != "25-35" {
		t.Fatalf("range pointer aliases prospective copy: %s", got)
	}
	if got := original.Clients[0].AWGParams.DNSServers[0]; got != "1.1.1.1" {
		t.Fatalf("DNS slice aliases prospective copy: %s", got)
	}
	if got := original.Clients[0].Routing.AllowedIPs[0]; got != "198.51.100.0/24" {
		t.Fatalf("routing slice aliases prospective copy: %s", got)
	}
}

func TestGenerateHeaderKeyIDUsesOpaqueRandomHex(t *testing.T) {
	first, err := generateHeaderKeyID()
	if err != nil {
		t.Fatalf("generateHeaderKeyID() error = %v", err)
	}
	second, err := generateHeaderKeyID()
	if err != nil {
		t.Fatalf("generateHeaderKeyID() error = %v", err)
	}
	if first == second {
		t.Fatal("generated header key IDs are equal")
	}
	if !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(first) {
		t.Fatalf("header key ID is not opaque lower-case hex: %q", first)
	}
	if strings.Contains(first, syntheticStorageHeaderKey()) {
		t.Fatal("header key ID contains key material")
	}
}

func TestPrepareStorageDefaultsStagesMissingServerStateWithoutMutatingLoadedData(t *testing.T) {
	loaded := &StorageData{}

	prepared, err := PrepareStorageDefaults(loaded)
	if err != nil {
		t.Fatalf("PrepareStorageDefaults() error = %v", err)
	}
	if loaded.ServerPrivateKey != "" || loaded.GeneratedParams != nil {
		t.Fatalf("loaded state mutated: %+v", loaded)
	}
	if prepared.ServerPrivateKey == "" || prepared.GeneratedParams == nil || !prepared.needsNormalization {
		t.Fatalf("prepared state = %+v, want pending generated defaults", prepared)
	}
}

func TestPrepareStorageDefaultsRejectsIncompletePersistedClientState(t *testing.T) {
	loaded := &StorageData{Clients: []ClientData{{ID: "persisted"}}}

	_, err := PrepareStorageDefaults(loaded)
	if err == nil {
		t.Fatal("PrepareStorageDefaults() accepted persisted clients without server defaults")
	}
}

func mustStorageRange(t *testing.T, value string) *config.Uint16Range {
	t.Helper()

	parsed, err := config.ParseUint16Range(value)
	if err != nil {
		t.Fatalf("ParseUint16Range(%q) error = %v", value, err)
	}

	return &parsed
}

func syntheticStorageHeaderKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xa5}, 32))
}
