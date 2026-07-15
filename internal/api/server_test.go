package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/clients"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
	"github.com/stealthsurf-vpn/awg-server/internal/usage"
)

const testAPIToken = "test-api-token"

type fakePeer struct {
	params       awg.AWGParams
	presharedKey *[32]byte
	allowedIP    string
}

type fakeDevicePool struct {
	publicKey    [32]byte
	nextPort     int
	profilePorts map[string]int
	profilePeers map[string]int
	peers        map[[32]byte]fakePeer
	migrations   int
	addErr       error
	removeErr    error
	dumpErr      error
	portErr      error
}

func newFakeDevicePool(t *testing.T, listenPort int) *fakeDevicePool {
	t.Helper()

	privateKey, err := awg.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error = %v", err)
	}

	return &fakeDevicePool{
		publicKey:    awg.PublicKeyFromPrivate(privateKey),
		nextPort:     listenPort,
		profilePorts: make(map[string]int),
		profilePeers: make(map[string]int),
		peers:        make(map[[32]byte]fakePeer),
	}
}

func (p *fakeDevicePool) AddPeer(params awg.AWGParams, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	if p.addErr != nil {
		return p.addErr
	}

	key := params.Key()
	port, ok := p.profilePorts[key]

	if ok {
		if params.Port != 0 && params.Port != port {
			return awg.ErrProfilePortConflict
		}
	} else {
		port = params.Port
		if port == 0 {
			port = p.nextAvailablePort()
		}

		for existingKey, existingPort := range p.profilePorts {
			if existingKey != key && existingPort == port {
				return awg.ErrPortInUse
			}
		}

		p.profilePorts[key] = port
	}

	var copiedPSK *[32]byte
	if presharedKey != nil {
		value := *presharedKey
		copiedPSK = &value
	}

	p.peers[publicKey] = fakePeer{
		params:       params,
		presharedKey: copiedPSK,
		allowedIP:    allowedIP,
	}
	p.profilePeers[key]++

	return nil
}

func (p *fakeDevicePool) RemovePeer(params awg.AWGParams, publicKey [32]byte, allowedIP string) error {
	if p.removeErr != nil {
		return p.removeErr
	}

	peer, ok := p.peers[publicKey]
	if !ok || peer.allowedIP != allowedIP {
		return errors.New("peer not found")
	}

	delete(p.peers, publicKey)
	p.removeProfilePeer(peer.params.Key())

	return nil
}

func (p *fakeDevicePool) MigratePeer(oldParams, newParams awg.AWGParams, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	peer, ok := p.peers[publicKey]
	if !ok || peer.allowedIP != allowedIP || peer.params.Key() != oldParams.Key() {
		return errors.New("old peer not found")
	}
	oldKey := oldParams.Key()
	if oldKey == newParams.Key() {
		if oldParams.Port == newParams.Port {
			return nil
		}
		if p.profilePeers[oldKey] > 1 {
			return awg.ErrPortShared
		}
	}

	delete(p.peers, publicKey)
	p.removeProfilePeer(oldKey)

	if err := p.AddPeer(newParams, publicKey, presharedKey, allowedIP); err != nil {
		if rollbackErr := p.AddPeer(oldParams, publicKey, peer.presharedKey, allowedIP); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}

		return err
	}

	p.migrations++

	return nil
}

func (p *fakeDevicePool) PortForParams(params awg.AWGParams) (int, error) {
	if p.portErr != nil {
		return 0, p.portErr
	}

	port, ok := p.profilePorts[params.Key()]
	if !ok {
		return 0, errors.New("profile not found")
	}

	return port, nil
}

func (p *fakeDevicePool) PublicKey() [32]byte {
	return p.publicKey
}

func (p *fakeDevicePool) InterfaceNames() []string {
	keys := p.activeProfileKeys()

	names := make([]string, len(keys))
	for i := range keys {
		names[i] = fmt.Sprintf("awg%d", i)
	}

	return names
}

func (p *fakeDevicePool) ShowDump(ifName string) ([]awg.PeerDump, error) {
	if p.dumpErr != nil {
		return nil, p.dumpErr
	}

	var interfaceIndex int
	if _, err := fmt.Sscanf(ifName, "awg%d", &interfaceIndex); err != nil {
		return nil, fmt.Errorf("parse interface name %q: %w", ifName, err)
	}

	keys := p.activeProfileKeys()
	if interfaceIndex < 0 || interfaceIndex >= len(keys) {
		return nil, fmt.Errorf("unknown interface %q", ifName)
	}

	profileKey := keys[interfaceIndex]
	peers := make([]awg.PeerDump, 0, len(p.peers))

	for publicKey, peer := range p.peers {
		if peer.params.Key() != profileKey {
			continue
		}

		peers = append(peers, awg.PeerDump{
			PublicKey:     awg.KeyToBase64(publicKey),
			TransferRx:    4096,
			TransferTx:    8192,
			LastHandshake: time.Date(2026, time.July, 15, 12, 30, 0, 0, time.UTC),
		})
	}

	return peers, nil
}

func (p *fakeDevicePool) activeProfileKeys() []string {
	keys := make([]string, 0, len(p.profilePeers))

	for key, count := range p.profilePeers {
		if count > 0 {
			keys = append(keys, key)
		}
	}

	sort.Strings(keys)

	return keys
}

func (p *fakeDevicePool) nextAvailablePort() int {
	for {
		port := p.nextPort
		p.nextPort++

		inUse := false
		for _, existingPort := range p.profilePorts {
			if existingPort == port {
				inUse = true
				break
			}
		}

		if !inUse {
			return port
		}
	}
}

func (p *fakeDevicePool) removeProfilePeer(key string) {
	p.profilePeers[key]--
	if p.profilePeers[key] > 0 {
		return
	}

	delete(p.profilePeers, key)
	delete(p.profilePorts, key)
}

type apiFixture struct {
	handler   http.Handler
	collector *usage.Collector
	pool      *fakeDevicePool
}

func newAPIFixture(t *testing.T) apiFixture {
	t.Helper()
	t.Setenv("PATH", t.TempDir())

	dataDir := t.TempDir()
	cfg := &config.Config{
		APIToken:   testAPIToken,
		Address:    "10.77.0.1/24",
		Endpoint:   "vpn.example.test",
		ListenPort: 51820,
		HTTPPort:   7777,
		MTU:        1420,
		DNS:        "1.1.1.1",
		DataDir:    dataDir,
	}
	defaultParams := awg.AWGParams{
		MTU:  cfg.MTU,
		DNS:  cfg.DNS,
		Jc:   5,
		Jmin: 50,
		Jmax: 1000,
		S1:   15,
		S2:   72,
		H1:   "100000-200000",
		H2:   "1000000-2000000",
		H3:   "10000000-20000000",
		H4:   "100000000-200000000",
	}

	privateKey, err := awg.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey() error = %v", err)
	}

	data := &clients.StorageData{
		ServerPrivateKey: awg.KeyToBase64(privateKey),
		GeneratedParams: &awg.GeneratedParams{
			H1: defaultParams.H1,
			H2: defaultParams.H2,
			H3: defaultParams.H3,
			H4: defaultParams.H4,
			S1: defaultParams.S1,
			S2: defaultParams.S2,
		},
	}
	pool := newFakeDevicePool(t, cfg.ListenPort)
	manager, err := clients.NewManager(pool, clients.NewStorage(dataDir), cfg, defaultParams, data)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	collector := usage.NewCollector(dataDir, pool.InterfaceNames, pool.ShowDump)
	server := NewServer(manager, cfg, collector)

	return apiFixture{
		handler:   server.httpServer.Handler,
		collector: collector,
		pool:      pool,
	}
}

func TestServerAPIFlow(t *testing.T) {
	fixture := newAPIFixture(t)

	health := performRequest(t, fixture.handler, http.MethodGet, "/health", "", false)
	assertStatus(t, health, http.StatusOK)
	assertContentType(t, health, "application/json")
	assertJSONField(t, health, "status", "ok")

	generated := performRequest(t, fixture.handler, http.MethodPost, "/api/awg-params/generate", "ignored", true)
	assertStatus(t, generated, http.StatusOK)

	var generatedParams awg.GeneratedParams
	decodeJSON(t, generated, &generatedParams)
	if generatedParams.H1 == "" || generatedParams.H2 == "" || generatedParams.H3 == "" || generatedParams.H4 == "" {
		t.Fatalf("generated params contain an empty H range: %+v", generatedParams)
	}
	if generatedParams.S1 < 15 || generatedParams.S1 > 150 || generatedParams.S2 < 15 || generatedParams.S2 > 150 {
		t.Fatalf("generated S values are out of range: %+v", generatedParams)
	}
	if generatedParams.S2 == generatedParams.S1+56 {
		t.Fatalf("generated S2 equals S1 + 56: %+v", generatedParams)
	}

	emptyList := performRequest(t, fixture.handler, http.MethodGet, "/api/clients", "ignored", true)
	assertStatus(t, emptyList, http.StatusOK)

	var initialClients []clientResponse
	decodeJSON(t, emptyList, &initialClients)
	if len(initialClients) != 0 {
		t.Fatalf("initial client count = %d, want 0", len(initialClients))
	}

	createBody := `{
		"id":"api-client",
		"awg_params":{
			"client_listen_port":51830,
			"mtu":1380,
			"dns_mode":"custom",
			"dns_servers":["1.1.1.1","1.0.0.1","1.1.1.1"],
			"persistent_keepalive":0
		},
		"routing":{"mode":"bypass","excluded_ips":["192.168.1.7/16","192.168.0.0/16"]}
	}`
	created := performRequest(t, fixture.handler, http.MethodPost, "/api/clients", createBody, true)
	assertStatus(t, created, http.StatusCreated)
	assertContentType(t, created, "application/json")
	assertResponseDoesNotExposeSecrets(t, created)

	var createdClient clientResponse
	decodeJSON(t, created, &createdClient)
	if createdClient.ID != "api-client" || createdClient.Address != "10.77.0.2" {
		t.Fatalf("created client = %+v", createdClient)
	}
	if createdClient.Routing.Mode != clients.RoutingModeBypass {
		t.Fatalf("created routing mode = %q", createdClient.Routing.Mode)
	}
	if len(createdClient.Routing.ExcludedIPs) != 1 || createdClient.Routing.ExcludedIPs[0] != "192.168.0.0/16" {
		t.Fatalf("created excluded IPs = %v", createdClient.Routing.ExcludedIPs)
	}
	if createdClient.AWGParams == nil || len(createdClient.AWGParams.DNSServers) != 2 {
		t.Fatalf("created AWG params = %+v", createdClient.AWGParams)
	}

	duplicate := performRequest(t, fixture.handler, http.MethodPost, "/api/clients", createBody, true)
	assertStatus(t, duplicate, http.StatusConflict)

	listed := performRequest(t, fixture.handler, http.MethodGet, "/api/clients", "ignored", true)
	assertStatus(t, listed, http.StatusOK)

	var listedRaw []map[string]any
	decodeJSON(t, listed, &listedRaw)
	if len(listedRaw) != 1 {
		t.Fatalf("listed client count = %d, want 1", len(listedRaw))
	}
	for _, field := range []string{"private_key", "public_key", "preshared_key"} {
		if _, ok := listedRaw[0][field]; ok {
			t.Fatalf("list response exposes %s", field)
		}
	}

	configuration := performRequest(t, fixture.handler, http.MethodGet, "/api/clients/api-client/configuration", "", true)
	assertStatus(t, configuration, http.StatusOK)
	assertContentType(t, configuration, "text/plain")
	assertBodyContains(t, configuration, "ListenPort = 51830")
	assertBodyContains(t, configuration, "DNS = 1.1.1.1, 1.0.0.1")
	assertBodyContains(t, configuration, "MTU = 1380")
	assertBodyContains(t, configuration, "Endpoint = vpn.example.test:51820")
	assertBodyContains(t, configuration, "PersistentKeepalive = 0")
	assertBodyContains(t, configuration, "AllowedIPs = 0.0.0.0/1, 128.0.0.0/2, 192.0.0.0/9, 192.128.0.0/11, 192.160.0.0/13, 192.169.0.0/16, 192.170.0.0/15, 192.172.0.0/14, 192.176.0.0/12, 192.192.0.0/10, 193.0.0.0/8, 194.0.0.0/7, 196.0.0.0/6, 200.0.0.0/5, 208.0.0.0/4, 224.0.0.0/3, ::/0")

	presharedKey := configurationValue(t, configuration, "PresharedKey")
	if _, err := awg.Base64ToKey(presharedKey); err != nil {
		t.Fatalf("configuration PresharedKey is invalid: %v", err)
	}

	zeroStats := performRequest(t, fixture.handler, http.MethodGet, "/api/clients/api-client/stats", "", true)
	assertStatus(t, zeroStats, http.StatusOK)

	var zeroStatsBody statsResponse
	decodeJSON(t, zeroStats, &zeroStatsBody)
	if zeroStatsBody.RxBytes != 0 || zeroStatsBody.TxBytes != 0 || zeroStatsBody.LastHandshake != "" {
		t.Fatalf("initial stats = %+v, want zero counters without handshake", zeroStatsBody)
	}

	fixture.collector.Collect()
	stats := performRequest(t, fixture.handler, http.MethodGet, "/api/clients/api-client/stats", "", true)
	assertStatus(t, stats, http.StatusOK)

	var statsBody statsResponse
	decodeJSON(t, stats, &statsBody)
	if statsBody.RxBytes != 4096 || statsBody.TxBytes != 8192 {
		t.Fatalf("stats = %+v", statsBody)
	}
	if statsBody.LastHandshake != "2026-07-15T12:30:00Z" {
		t.Fatalf("last handshake = %q", statsBody.LastHandshake)
	}

	updateBody := `{
		"awg_params":{"dns_mode":"system"},
		"routing":{
			"mode":"split",
			"allowed_ips":["10.1.2.3/8","172.16.0.0/12"],
			"excluded_ips":["10.10.0.0/16"]
		}
	}`
	updated := performRequest(t, fixture.handler, http.MethodPatch, "/api/clients/api-client", updateBody, true)
	assertStatus(t, updated, http.StatusOK)
	assertResponseDoesNotExposeSecrets(t, updated)

	var updatedClient clientResponse
	decodeJSON(t, updated, &updatedClient)
	if updatedClient.AWGParams == nil || updatedClient.AWGParams.DNSMode != awg.DNSModeSystem {
		t.Fatalf("updated AWG params = %+v", updatedClient.AWGParams)
	}
	if updatedClient.Routing.Mode != clients.RoutingModeSplit {
		t.Fatalf("updated routing = %+v", updatedClient.Routing)
	}
	if fixture.pool.migrations != 0 {
		t.Fatalf("client-only update migration count = %d, want 0", fixture.pool.migrations)
	}

	updatedConfiguration := performRequest(t, fixture.handler, http.MethodGet, "/api/clients/api-client/configuration", "", true)
	assertStatus(t, updatedConfiguration, http.StatusOK)
	assertBodyNotContains(t, updatedConfiguration, "\nDNS = ")
	assertBodyNotContains(t, updatedConfiguration, "ListenPort = ")
	assertBodyContains(t, updatedConfiguration, "MTU = 1420")
	assertBodyContains(t, updatedConfiguration, "PersistentKeepalive = 25")
	assertBodyContains(t, updatedConfiguration, "AllowedIPs = 10.0.0.0/13, 10.8.0.0/15, 10.11.0.0/16, 10.12.0.0/14, 10.16.0.0/12, 10.32.0.0/11, 10.64.0.0/10, 10.128.0.0/9, 172.16.0.0/12")

	regenerated := performRequest(t, fixture.handler, http.MethodPost, "/api/clients/api-client/regenerate-awg-params", "ignored", true)
	assertStatus(t, regenerated, http.StatusOK)
	assertResponseDoesNotExposeSecrets(t, regenerated)

	var regeneratedClient clientResponse
	decodeJSON(t, regenerated, &regeneratedClient)
	if regeneratedClient.AWGParams == nil || regeneratedClient.AWGParams.H1 == "" || regeneratedClient.AWGParams.H4 == "" {
		t.Fatalf("regenerated AWG params = %+v", regeneratedClient.AWGParams)
	}
	if regeneratedClient.AWGParams.DNSMode != awg.DNSModeSystem {
		t.Fatalf("regeneration did not retain DNS mode: %+v", regeneratedClient.AWGParams)
	}
	if regeneratedClient.ID != updatedClient.ID || regeneratedClient.Address != updatedClient.Address {
		t.Fatalf("regeneration changed client identity: before=%+v after=%+v", updatedClient, regeneratedClient)
	}
	if regeneratedClient.Routing.Mode != updatedClient.Routing.Mode {
		t.Fatalf("regeneration changed routing: before=%+v after=%+v", updatedClient.Routing, regeneratedClient.Routing)
	}
	if fixture.pool.migrations != 1 {
		t.Fatalf("regeneration migration count = %d, want 1", fixture.pool.migrations)
	}

	reset := performRequest(t, fixture.handler, http.MethodPatch, "/api/clients/api-client", `{"awg_params":null,"routing":null}`, true)
	assertStatus(t, reset, http.StatusOK)
	assertResponseDoesNotExposeSecrets(t, reset)

	var resetClient clientResponse
	decodeJSON(t, reset, &resetClient)
	if resetClient.AWGParams != nil || resetClient.Routing.Mode != clients.RoutingModeFull {
		t.Fatalf("reset client = %+v", resetClient)
	}
	if fixture.pool.migrations != 2 {
		t.Fatalf("reset migration count = %d, want 2", fixture.pool.migrations)
	}

	resetConfiguration := performRequest(t, fixture.handler, http.MethodGet, "/api/clients/api-client/configuration", "", true)
	assertStatus(t, resetConfiguration, http.StatusOK)
	assertBodyContains(t, resetConfiguration, "DNS = 1.1.1.1")
	assertBodyContains(t, resetConfiguration, "AllowedIPs = 0.0.0.0/0, ::/0")

	publicKey := onlyPeerPublicKey(t, fixture.pool)

	deleted := performRequest(t, fixture.handler, http.MethodDelete, "/api/clients/api-client", "ignored", true)
	assertStatus(t, deleted, http.StatusNoContent)
	if deleted.Body.Len() != 0 {
		t.Fatalf("delete response body = %q, want empty", deleted.Body.String())
	}

	afterDelete := performRequest(t, fixture.handler, http.MethodGet, "/api/clients", "", true)
	assertStatus(t, afterDelete, http.StatusOK)

	var remaining []clientResponse
	decodeJSON(t, afterDelete, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("remaining client count = %d, want 0", len(remaining))
	}
	if len(fixture.pool.peers) != 0 || len(fixture.pool.profilePorts) != 0 {
		t.Fatalf("device state remains after delete: peers=%d profiles=%d", len(fixture.pool.peers), len(fixture.pool.profilePorts))
	}
	if _, ok := fixture.collector.GetStats(awg.KeyToBase64(publicKey)); ok {
		t.Fatal("usage stats remain after delete")
	}

	missingStats := performRequest(t, fixture.handler, http.MethodGet, "/api/clients/api-client/stats", "", true)
	assertStatus(t, missingStats, http.StatusNotFound)
}

func TestServerAuthentication(t *testing.T) {
	fixture := newAPIFixture(t)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "generate", method: http.MethodPost, path: "/api/awg-params/generate"},
		{name: "list", method: http.MethodGet, path: "/api/clients"},
		{name: "create", method: http.MethodPost, path: "/api/clients", body: `{"id":"auth-client"}`},
		{name: "update", method: http.MethodPatch, path: "/api/clients/auth-client", body: `{"routing":null}`},
		{name: "regenerate", method: http.MethodPost, path: "/api/clients/auth-client/regenerate-awg-params"},
		{name: "configuration", method: http.MethodGet, path: "/api/clients/auth-client/configuration"},
		{name: "stats", method: http.MethodGet, path: "/api/clients/auth-client/stats"},
		{name: "delete", method: http.MethodDelete, path: "/api/clients/auth-client"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			missing := performRequest(t, fixture.handler, tt.method, tt.path, tt.body, false)
			assertStatus(t, missing, http.StatusUnauthorized)
			assertJSONField(t, missing, "error", "missing authorization header")

			invalid := performRequestWithToken(t, fixture.handler, tt.method, tt.path, tt.body, "invalid-token")
			assertStatus(t, invalid, http.StatusUnauthorized)
			assertJSONField(t, invalid, "error", "invalid token")
		})
	}
}

func TestServerValidationAndMethodHandling(t *testing.T) {
	fixture := newAPIFixture(t)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "malformed create", method: http.MethodPost, path: "/api/clients", body: `{"id":`, status: http.StatusBadRequest},
		{name: "duplicate JSON create", method: http.MethodPost, path: "/api/clients", body: `{"id":"one"} {"id":"two"}`, status: http.StatusBadRequest},
		{name: "invalid DNS create", method: http.MethodPost, path: "/api/clients", body: `{"id":"bad-dns","awg_params":{"dns_mode":"custom","dns_servers":["https://dns.example"]}}`, status: http.StatusBadRequest},
		{name: "invalid routing create", method: http.MethodPost, path: "/api/clients", body: `{"id":"bad-route","routing":{"mode":"bypass","excluded_ips":[]}}`, status: http.StatusBadRequest},
		{name: "empty update", method: http.MethodPatch, path: "/api/clients/missing", body: `{}`, status: http.StatusBadRequest},
		{name: "missing update", method: http.MethodPatch, path: "/api/clients/missing", body: `{"routing":null}`, status: http.StatusNotFound},
		{name: "missing regenerate", method: http.MethodPost, path: "/api/clients/missing/regenerate-awg-params", status: http.StatusNotFound},
		{name: "missing configuration", method: http.MethodGet, path: "/api/clients/missing/configuration", status: http.StatusNotFound},
		{name: "missing stats", method: http.MethodGet, path: "/api/clients/missing/stats", status: http.StatusNotFound},
		{name: "missing delete", method: http.MethodDelete, path: "/api/clients/missing", status: http.StatusNotFound},
		{name: "unsupported method", method: http.MethodPut, path: "/api/clients", status: http.StatusMethodNotAllowed},
		{name: "unknown route", method: http.MethodGet, path: "/api/not-a-route", status: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := performRequest(t, fixture.handler, tt.method, tt.path, tt.body, true)
			assertStatus(t, response, tt.status)
		})
	}

	healthHead := performRequest(t, fixture.handler, http.MethodHead, "/health", "", false)
	assertStatus(t, healthHead, http.StatusOK)

	clientsHead := performRequest(t, fixture.handler, http.MethodHead, "/api/clients", "", true)
	assertStatus(t, clientsHead, http.StatusOK)
}

func TestServerOperationFailures(t *testing.T) {
	t.Run("create interface limit", func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.pool.addErr = awg.ErrMaxInterfacesReached

		response := performRequest(t, fixture.handler, http.MethodPost, "/api/clients", `{"id":"limited"}`, true)
		assertStatus(t, response, http.StatusServiceUnavailable)
		assertJSONField(t, response, "error", "add peer to device: "+awg.ErrMaxInterfacesReached.Error())
	})

	t.Run("create internal device error", func(t *testing.T) {
		fixture := newAPIFixture(t)
		fixture.pool.addErr = errors.New("sensitive device details")

		response := performRequest(t, fixture.handler, http.MethodPost, "/api/clients", `{"id":"device-error"}`, true)
		assertStatus(t, response, http.StatusInternalServerError)
		assertJSONField(t, response, "error", "internal server error")
		assertBodyNotContains(t, response, "sensitive device details")
	})

	t.Run("configuration internal error is hidden and logged", func(t *testing.T) {
		fixture := newAPIFixture(t)
		created := performRequest(t, fixture.handler, http.MethodPost, "/api/clients", `{"id":"configuration-error"}`, true)
		assertStatus(t, created, http.StatusCreated)

		fixture.pool.portErr = errors.New("sensitive configuration details")
		var logs bytes.Buffer
		previousLogOutput := log.Writer()
		log.SetOutput(&logs)
		t.Cleanup(func() { log.SetOutput(previousLogOutput) })

		response := performRequest(t, fixture.handler, http.MethodGet, "/api/clients/configuration-error/configuration", "", true)
		assertStatus(t, response, http.StatusInternalServerError)
		assertJSONField(t, response, "error", "internal server error")
		assertBodyNotContains(t, response, "sensitive configuration details")
		if !strings.Contains(logs.String(), "sensitive configuration details") {
			t.Fatalf("server log = %q, want internal error details", logs.String())
		}
	})

	t.Run("snapshot failure aborts regeneration", func(t *testing.T) {
		fixture := newAPIFixture(t)
		created := performRequest(t, fixture.handler, http.MethodPost, "/api/clients", `{"id":"snapshot"}`, true)
		assertStatus(t, created, http.StatusCreated)

		before := onlyPeer(t, fixture.pool)
		fixture.pool.dumpErr = errors.New("sensitive dump details")

		response := performRequest(t, fixture.handler, http.MethodPost, "/api/clients/snapshot/regenerate-awg-params", "", true)
		assertStatus(t, response, http.StatusInternalServerError)
		assertJSONField(t, response, "error", "internal server error")
		if fixture.pool.migrations != 0 {
			t.Fatalf("migration count = %d, want 0", fixture.pool.migrations)
		}

		after := onlyPeer(t, fixture.pool)
		if after.params.Key() != before.params.Key() || after.allowedIP != before.allowedIP {
			t.Fatalf("peer changed after snapshot failure: before=%+v after=%+v", before, after)
		}
	})

	t.Run("migration conflict", func(t *testing.T) {
		fixture := newAPIFixture(t)
		for _, id := range []string{"conflict", "shared-peer"} {
			created := performRequest(t, fixture.handler, http.MethodPost, "/api/clients", `{"id":"`+id+`"}`, true)
			assertStatus(t, created, http.StatusCreated)
		}

		beforePeers := maps.Clone(fixture.pool.peers)
		beforePorts := maps.Clone(fixture.pool.profilePorts)
		beforeCounts := maps.Clone(fixture.pool.profilePeers)
		response := performRequest(t, fixture.handler, http.MethodPatch, "/api/clients/conflict", `{"awg_params":{"port":51820}}`, true)
		assertStatus(t, response, http.StatusConflict)
		if fixture.pool.migrations != 0 {
			t.Fatalf("migration count = %d, want 0", fixture.pool.migrations)
		}
		if !reflect.DeepEqual(fixture.pool.peers, beforePeers) ||
			!reflect.DeepEqual(fixture.pool.profilePorts, beforePorts) ||
			!reflect.DeepEqual(fixture.pool.profilePeers, beforeCounts) {
			t.Fatalf("device state changed after shared-port conflict: peers=%+v ports=%+v counts=%+v", fixture.pool.peers, fixture.pool.profilePorts, fixture.pool.profilePeers)
		}

		listed := performRequest(t, fixture.handler, http.MethodGet, "/api/clients", "", true)
		assertStatus(t, listed, http.StatusOK)
		var clientsBody []clientResponse
		decodeJSON(t, listed, &clientsBody)
		if len(clientsBody) != 2 {
			t.Fatalf("client count after shared-port conflict = %d, want 2", len(clientsBody))
		}
		for _, client := range clientsBody {
			if client.AWGParams != nil {
				t.Fatalf("client %q changed after shared-port conflict: %+v", client.ID, client.AWGParams)
			}
		}
	})

	t.Run("delete internal device error", func(t *testing.T) {
		fixture := newAPIFixture(t)
		created := performRequest(t, fixture.handler, http.MethodPost, "/api/clients", `{"id":"delete-error"}`, true)
		assertStatus(t, created, http.StatusCreated)
		fixture.pool.removeErr = errors.New("sensitive remove details")

		response := performRequest(t, fixture.handler, http.MethodDelete, "/api/clients/delete-error", "", true)
		assertStatus(t, response, http.StatusInternalServerError)
		assertJSONField(t, response, "error", "internal server error")

		listed := performRequest(t, fixture.handler, http.MethodGet, "/api/clients", "", true)
		assertStatus(t, listed, http.StatusOK)

		var clientsBody []clientResponse
		decodeJSON(t, listed, &clientsBody)
		if len(clientsBody) != 1 || clientsBody[0].ID != "delete-error" {
			t.Fatalf("client state after failed delete = %+v", clientsBody)
		}
	})
}

func TestClientUpdateErrorStatus(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "rollback", err: awg.ErrRollbackFailed, status: http.StatusInternalServerError},
		{name: "not found", err: clients.ErrClientNotFound, status: http.StatusNotFound},
		{name: "invalid params", err: awg.ErrInvalidParams, status: http.StatusBadRequest},
		{name: "invalid port", err: awg.ErrInvalidPort, status: http.StatusBadRequest},
		{name: "invalid routing", err: clients.ErrInvalidRouting, status: http.StatusBadRequest},
		{name: "empty update", err: clients.ErrEmptyClientUpdate, status: http.StatusBadRequest},
		{name: "port in use", err: awg.ErrPortInUse, status: http.StatusConflict},
		{name: "shared port", err: awg.ErrPortShared, status: http.StatusConflict},
		{name: "profile port conflict", err: awg.ErrProfilePortConflict, status: http.StatusConflict},
		{name: "interface limit", err: awg.ErrMaxInterfacesReached, status: http.StatusServiceUnavailable},
		{name: "unknown", err: errors.New("unknown"), status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clientUpdateErrorStatus(fmt.Errorf("wrapped: %w", tt.err)); got != tt.status {
				t.Fatalf("clientUpdateErrorStatus() = %d, want %d", got, tt.status)
			}
		})
	}
}

func performRequest(t *testing.T, handler http.Handler, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()

	token := ""
	if authenticated {
		token = testAPIToken
	}

	return performRequestWithToken(t, handler, method, path, body, token)
}

func performRequestWithToken(t *testing.T, handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()

	if response.Code != want {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, want, response.Body.String())
	}
}

func assertContentType(t *testing.T, response *httptest.ResponseRecorder, prefix string) {
	t.Helper()

	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, prefix) {
		t.Fatalf("Content-Type = %q, want prefix %q", got, prefix)
	}
}

func assertJSONField(t *testing.T, response *httptest.ResponseRecorder, field, want string) {
	t.Helper()

	var body map[string]string
	decodeJSON(t, response, &body)

	if body[field] != want {
		t.Fatalf("JSON field %q = %q, want %q", field, body[field], want)
	}
}

func assertResponseDoesNotExposeSecrets(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()

	var body map[string]any
	decodeJSON(t, response, &body)

	for _, field := range []string{"private_key", "public_key", "preshared_key"} {
		if _, ok := body[field]; ok {
			t.Fatalf("response exposes %s", field)
		}
	}
}

func decodeJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()

	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode JSON response %q: %v", response.Body.String(), err)
	}
}

func assertBodyContains(t *testing.T, response *httptest.ResponseRecorder, value string) {
	t.Helper()

	if !strings.Contains(response.Body.String(), value) {
		t.Fatalf("response body does not contain %q:\n%s", value, response.Body.String())
	}
}

func assertBodyNotContains(t *testing.T, response *httptest.ResponseRecorder, value string) {
	t.Helper()

	if strings.Contains(response.Body.String(), value) {
		t.Fatalf("response body contains %q:\n%s", value, response.Body.String())
	}
}

func configurationValue(t *testing.T, response *httptest.ResponseRecorder, key string) string {
	t.Helper()

	prefix := key + " = "
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}

	t.Fatalf("configuration does not contain %s", key)
	return ""
}

func onlyPeerPublicKey(t *testing.T, pool *fakeDevicePool) [32]byte {
	t.Helper()

	if len(pool.peers) != 1 {
		t.Fatalf("peer count = %d, want 1", len(pool.peers))
	}

	for publicKey := range pool.peers {
		return publicKey
	}

	return [32]byte{}
}

func onlyPeer(t *testing.T, pool *fakeDevicePool) fakePeer {
	t.Helper()

	publicKey := onlyPeerPublicKey(t, pool)
	return pool.peers[publicKey]
}
