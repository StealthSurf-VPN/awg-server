package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/clients"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
	"github.com/stealthsurf-vpn/awg-server/internal/usage"
)

const testAPIToken = "test-token"

type apiSmokePool struct {
	serverPublicKey [32]byte
	peerPublicKey   [32]byte
	profileKey      awg.ProfileKey
	peerPort        int
	hasPeer         bool
	migrations      int
	addErr          error
	dumpErr         error
	removeErr       error
	migrateErr      error
	portErr         error
	firewallErrAt   int
	firewallCallNum int
	firewallCalls   [][]awg.LANPeer
	activeLANPeers  []awg.LANPeer
}

func (p *apiSmokePool) AddPeer(profile awg.Profile, requestedPort int, publicKey [32]byte, _ *[32]byte, _ string) error {
	if p.addErr != nil {
		return p.addErr
	}

	p.peerPublicKey = publicKey
	p.profileKey = profile.Key()
	p.peerPort = requestedPort
	p.hasPeer = true

	return nil
}

func (p *apiSmokePool) RemovePeer(_ awg.Profile, _ [32]byte, _ string) error {
	if p.removeErr != nil {
		return p.removeErr
	}

	p.hasPeer = false

	return nil
}

func (p *apiSmokePool) MigratePeer(_, newProfile awg.Profile, requestedPort int, publicKey [32]byte, _ *[32]byte, _ string) error {
	if p.migrateErr != nil {
		return p.migrateErr
	}

	p.peerPublicKey = publicKey
	p.profileKey = newProfile.Key()
	p.peerPort = requestedPort
	p.hasPeer = true
	p.migrations++

	return nil
}

func (p *apiSmokePool) PortForProfile(awg.Profile) (int, error) {
	if p.portErr != nil {
		return 0, p.portErr
	}

	return 51820, nil
}

func (p *apiSmokePool) PublicKey() [32]byte {
	return p.serverPublicKey
}

func (p *apiSmokePool) ApplyLANIsolation(peers []awg.LANPeer) error {
	copyPeers := append([]awg.LANPeer(nil), peers...)
	p.firewallCalls = append(p.firewallCalls, copyPeers)
	p.firewallCallNum++

	if p.firewallErrAt == p.firewallCallNum {
		return errors.New("sensitive firewall details")
	}

	p.activeLANPeers = copyPeers
	return nil
}

func (p *apiSmokePool) interfaceNames() []string {
	if !p.hasPeer {
		return nil
	}

	return []string{"awg0"}
}

func (p *apiSmokePool) showDump(string) ([]awg.PeerDump, error) {
	if p.dumpErr != nil {
		return nil, p.dumpErr
	}
	if !p.hasPeer {
		return nil, nil
	}

	return []awg.PeerDump{{
		PublicKey:  awg.KeyToBase64(p.peerPublicKey),
		TransferRx: 1024,
		TransferTx: 2048,
	}}, nil
}

func TestProtectedRoutesRequireAuthorization(t *testing.T) {
	routes := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/awg-params/generate"},
		{method: http.MethodGet, path: "/api/capabilities"},
		{method: http.MethodGet, path: "/api/clients"},
		{method: http.MethodPost, path: "/api/clients"},
		{method: http.MethodPatch, path: "/api/clients/lan-group"},
		{method: http.MethodGet, path: "/api/clients/client/configuration"},
		{method: http.MethodPatch, path: "/api/clients/client"},
		{method: http.MethodPost, path: "/api/clients/client/regenerate-awg-params"},
		{method: http.MethodGet, path: "/api/clients/client/stats"},
		{method: http.MethodDelete, path: "/api/clients/client"},
	}

	server := NewServer(nil, &config.Config{APIToken: testAPIToken}, nil)

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, nil)
			response := httptest.NewRecorder()

			server.httpServer.Handler.ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestAPICapabilities(t *testing.T) {
	handler, _, _ := newAuthorizedAPISmoke(t)

	response := authorizedAPIRequest(t, handler, http.MethodGet, "/api/capabilities", "")
	assertAPIStatus(t, response, http.StatusOK)

	var body map[string]bool
	decodeAPIResponse(t, response, &body)
	if len(body) != 2 || !body["lan_group_isolation"] || !body["awg_protocol_3_1"] {
		t.Fatalf("capabilities = %+v", body)
	}
}

func TestAPICreateProtocolVersionBoundary(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantStatus  int
		wantVersion string
	}{
		{name: "omitted uses configured default", body: `{"id":"client"}`, wantStatus: http.StatusCreated, wantVersion: "3.1"},
		{name: "legacy alias", body: `{"id":"client","protocol_version":"2"}`, wantStatus: http.StatusCreated, wantVersion: "2.0"},
		{name: "canonical legacy", body: `{"id":"client","protocol_version":"2.0"}`, wantStatus: http.StatusCreated, wantVersion: "2.0"},
		{name: "canonical AWG 3.1", body: `{"id":"client","protocol_version":"3.1"}`, wantStatus: http.StatusCreated, wantVersion: "3.1"},
		{name: "null", body: `{"id":"client","protocol_version":null}`, wantStatus: http.StatusBadRequest},
		{name: "unknown", body: `{"id":"client","protocol_version":"3"}`, wantStatus: http.StatusBadRequest},
		{name: "non string", body: `{"id":"client","protocol_version":3.1}`, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _, pool := newAuthorizedAPISmoke(t)

			response := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", tt.body)
			assertAPIStatus(t, response, tt.wantStatus)

			if tt.wantStatus == http.StatusCreated {
				assertPublicClientPayload(t, response.Body.Bytes(), tt.wantVersion)
				return
			}

			if pool.hasPeer {
				t.Fatal("invalid protocol version created a peer")
			}

			listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
			assertAPIStatus(t, listed, http.StatusOK)

			var clients []json.RawMessage
			decodeAPIResponse(t, listed, &clients)
			if len(clients) != 0 {
				t.Fatalf("invalid protocol version created clients: %s", listed.Body.String())
			}
		})
	}
}

func TestAPIProtocolVersionErrorDoesNotReflectSecretLikeInput(t *testing.T) {
	handler, _, pool := newAuthorizedAPISmoke(t)
	secret := apiAWG31Storage().HeaderKeys["api-synthetic-default-id"].HeaderProtectionKey

	response := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"client","protocol_version":"`+secret+`"}`)
	assertAPIStatus(t, response, http.StatusBadRequest)
	if strings.Contains(response.Body.String(), secret) {
		t.Fatalf("protocol_version error reflects secret-like input: %s", response.Body.String())
	}
	if pool.hasPeer {
		t.Fatal("invalid protocol version created a peer")
	}
}

func TestAPIAWG31OverridesResolveAgainstTargetProtocol(t *testing.T) {
	t.Run("POST and omitted-version PATCH accept AWG 3.1 ranges and toggles", func(t *testing.T) {
		handler, _, _ := newAuthorizedAPISmoke(t)

		created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{
			"id":"v31",
			"protocol_version":"3.1",
			"awg_params":{
				"persistent_keepalive":"25-35",
				"content_padding_addition":"10-100",
				"rekey_after_time":"100-120",
				"rekey_timeout":"3-7",
				"reject_after_time":"150-180",
				"keepalive_timeout":"5-15",
				"max_handshake_attempts":"15-20",
				"random_trailers":"off",
				"disable_cookies":"on"
			}
		}`)
		assertAPIStatus(t, created, http.StatusCreated)
		assertPublicClientPayload(t, created.Body.Bytes(), "3.1")

		updated := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/v31", `{
			"awg_params":{
				"persistent_keepalive":"off",
				"content_padding_addition":"11-12",
				"random_trailers":"on"
			}
		}`)
		assertAPIStatus(t, updated, http.StatusOK)
		assertPublicClientPayload(t, updated.Body.Bytes(), "3.1")
	})

	t.Run("legacy rejects AWG 3.1-only values before mutation", func(t *testing.T) {
		handler, _, pool := newAuthorizedAPISmoke(t)

		response := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{
			"id":"legacy",
			"protocol_version":"2.0",
			"awg_params":{"content_padding_addition":"10-100","persistent_keepalive":"off"}
		}`)
		assertAPIStatus(t, response, http.StatusBadRequest)
		if pool.hasPeer {
			t.Fatal("invalid legacy values created a peer")
		}

		listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
		assertAPIStatus(t, listed, http.StatusOK)

		var clients []json.RawMessage
		decodeAPIResponse(t, listed, &clients)
		if len(clients) != 0 {
			t.Fatalf("invalid legacy values created clients: %s", listed.Body.String())
		}
	})
}

func TestAPIRejectsExplicitNestedAWGParamsNullBeforeMutation(t *testing.T) {
	fields := []string{
		"persistent_keepalive",
		"content_padding_addition",
		"rekey_after_time",
		"rekey_timeout",
		"reject_after_time",
		"keepalive_timeout",
		"max_handshake_attempts",
		"random_trailers",
		"disable_cookies",
	}
	const secret = "secret-like-input-must-not-appear"

	for _, version := range []string{"2.0", "3.1"} {
		for _, field := range fields {
			t.Run(version+"/"+field, func(t *testing.T) {
				handler, _, pool, storage := newAuthorizedAPISmokeWithStorage(t)

				created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"client","protocol_version":"`+version+`"}`)
				assertAPIStatus(t, created, http.StatusCreated)

				beforeClients := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
				assertAPIStatus(t, beforeClients, http.StatusOK)
				beforeStorage, err := storage.Load()
				if err != nil {
					t.Fatalf("Load() before invalid PATCH error = %v", err)
				}
				beforeStorageJSON, err := json.Marshal(beforeStorage)
				if err != nil {
					t.Fatalf("Marshal() before invalid PATCH error = %v", err)
				}
				beforeProfile := pool.profileKey

				response := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/client", `{"awg_params":{"`+strings.ToUpper(field)+`":null,"unknown_field":"`+secret+`"}}`)
				assertAPIStatus(t, response, http.StatusBadRequest)
				if strings.Contains(response.Body.String(), secret) {
					t.Fatalf("invalid null response reflects secret-like input: %s", response.Body.String())
				}
				if !pool.hasPeer || pool.migrations != 0 || pool.profileKey != beforeProfile {
					t.Fatalf("invalid null PATCH mutated pool: hasPeer=%t migrations=%d profile=%v", pool.hasPeer, pool.migrations, pool.profileKey)
				}

				afterClients := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
				assertAPIStatus(t, afterClients, http.StatusOK)
				if !bytes.Equal(beforeClients.Body.Bytes(), afterClients.Body.Bytes()) {
					t.Fatalf("invalid null PATCH mutated manager state: before=%s after=%s", beforeClients.Body.String(), afterClients.Body.String())
				}

				afterStorage, err := storage.Load()
				if err != nil {
					t.Fatalf("Load() after invalid PATCH error = %v", err)
				}
				afterStorageJSON, err := json.Marshal(afterStorage)
				if err != nil {
					t.Fatalf("Marshal() after invalid PATCH error = %v", err)
				}
				if !bytes.Equal(beforeStorageJSON, afterStorageJSON) {
					t.Fatalf("invalid null PATCH mutated storage: before=%s after=%s", beforeStorageJSON, afterStorageJSON)
				}
			})
		}
	}
}

func TestAPIProtocolVersionPatchUsesOneManagerTransaction(t *testing.T) {
	handler, _, pool := newAuthorizedAPISmoke(t)

	created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"migrate"}`)
	assertAPIStatus(t, created, http.StatusCreated)
	assertPublicClientPayload(t, created.Body.Bytes(), "3.1")

	updated := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/migrate", `{
		"protocol_version":"2",
		"awg_params":{"persistent_keepalive":0},
		"routing":{"mode":"split","allowed_ips":["10.1.2.3/8"]}
	}`)
	assertAPIStatus(t, updated, http.StatusOK)
	assertPublicClientPayload(t, updated.Body.Bytes(), "2.0")
	if pool.migrations != 1 {
		t.Fatalf("version/params/routing migrations = %d, want 1", pool.migrations)
	}
}

func TestAPIProtocolVersionPatchAloneAndParamsReset(t *testing.T) {
	t.Run("version alone is a non-empty migration", func(t *testing.T) {
		handler, _, pool := newAuthorizedAPISmoke(t)

		created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"migrate"}`)
		assertAPIStatus(t, created, http.StatusCreated)

		updated := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/migrate", `{"protocol_version":"2.0"}`)
		assertAPIStatus(t, updated, http.StatusOK)
		assertPublicClientPayload(t, updated.Body.Bytes(), "2.0")
		if pool.migrations != 1 {
			t.Fatalf("version-only migrations = %d, want 1", pool.migrations)
		}
	})

	t.Run("AWG params null resets while preserving protocol version", func(t *testing.T) {
		handler, _, _ := newAuthorizedAPISmoke(t)

		created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{
			"id":"reset",
			"awg_params":{"content_padding_addition":"11-12"}
		}`)
		assertAPIStatus(t, created, http.StatusCreated)

		reset := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/reset", `{"awg_params":null}`)
		assertAPIStatus(t, reset, http.StatusOK)
		assertPublicClientPayload(t, reset.Body.Bytes(), "3.1")

		var body map[string]json.RawMessage
		decodeAPIResponse(t, reset, &body)
		if _, exists := body["awg_params"]; exists {
			t.Fatalf("reset response retains awg_params: %s", reset.Body.String())
		}
	})
}

func TestAPIRejectsInvalidPatchProtocolVersionsBeforeMutation(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "null", body: `{"protocol_version":null}`},
		{name: "unknown", body: `{"protocol_version":"3"}`},
		{name: "non string", body: `{"protocol_version":3.1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _, pool := newAuthorizedAPISmoke(t)

			created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"client"}`)
			assertAPIStatus(t, created, http.StatusCreated)
			originalProfile := pool.profileKey

			response := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/client", tt.body)
			assertAPIStatus(t, response, http.StatusBadRequest)
			if pool.migrations != 0 || pool.profileKey != originalProfile {
				t.Fatalf("invalid protocol version mutated peer: migrations=%d profile=%v", pool.migrations, pool.profileKey)
			}

			listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
			assertAPIStatus(t, listed, http.StatusOK)

			var clients []json.RawMessage
			decodeAPIResponse(t, listed, &clients)
			if len(clients) != 1 {
				t.Fatalf("clients after invalid PATCH = %s", listed.Body.String())
			}
			assertPublicClientPayload(t, clients[0], "3.1")
		})
	}
}

func TestAPILegacyPatchRejectsAWG31ValuesBeforeMutation(t *testing.T) {
	handler, _, pool := newAuthorizedAPISmoke(t)

	created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"client"}`)
	assertAPIStatus(t, created, http.StatusCreated)
	originalProfile := pool.profileKey

	response := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/client", `{
		"protocol_version":"2.0",
		"awg_params":{"content_padding_addition":"10-100","persistent_keepalive":"off"}
	}`)
	assertAPIStatus(t, response, http.StatusBadRequest)
	if pool.migrations != 0 || pool.profileKey != originalProfile {
		t.Fatalf("invalid legacy PATCH mutated peer: migrations=%d profile=%v", pool.migrations, pool.profileKey)
	}

	listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
	assertAPIStatus(t, listed, http.StatusOK)

	var clients []json.RawMessage
	decodeAPIResponse(t, listed, &clients)
	if len(clients) != 1 {
		t.Fatalf("clients after invalid legacy PATCH = %s", listed.Body.String())
	}
	assertPublicClientPayload(t, clients[0], "3.1")
}

func TestAPIGenerateAWGParamsSelectsConfiguredProtocol(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		version string
	}{
		{name: "configured default", path: "/api/awg-params/generate", version: "3.1"},
		{name: "legacy alias", path: "/api/awg-params/generate?protocol_version=2", version: "2.0"},
		{name: "canonical legacy", path: "/api/awg-params/generate?protocol_version=2.0", version: "2.0"},
		{name: "canonical AWG 3.1", path: "/api/awg-params/generate?protocol_version=3.1", version: "3.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _, _ := newAuthorizedAPISmoke(t)

			response := authorizedAPIRequest(t, handler, http.MethodPost, tt.path, "")
			assertAPIStatus(t, response, http.StatusOK)
			assertGeneratedParamsForProtocol(t, response.Body.Bytes(), tt.version)
		})
	}

	for _, path := range []string{
		"/api/awg-params/generate?protocol_version=3",
		"/api/awg-params/generate?protocol_version=2&protocol_version=3.1",
		"/api/awg-params/generate?protocol_versoin=2",
		"/api/awg-params/generate?protocol_version=2&protocol_versoin=3.1",
	} {
		handler, _, _ := newAuthorizedAPISmoke(t)

		response := authorizedAPIRequest(t, handler, http.MethodPost, path, "")
		assertAPIStatus(t, response, http.StatusBadRequest)
	}

	handler, _, _ := newAuthorizedAPISmoke(t)
	request := httptest.NewRequest(http.MethodPost, "/api/awg-params/generate", nil)
	request.URL.RawQuery = "protocol_version=%ZZ"
	request.Header.Set("Authorization", "Bearer "+testAPIToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertAPIStatus(t, response, http.StatusBadRequest)
}

func TestAPIClientResponsesIncludeProtocolVersionAndRedactSecrets(t *testing.T) {
	handler, _, _ := newAuthorizedAPISmoke(t)

	created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"public"}`)
	assertAPIStatus(t, created, http.StatusCreated)
	assertPublicClientPayload(t, created.Body.Bytes(), "3.1")

	listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
	assertAPIStatus(t, listed, http.StatusOK)

	var listedClients []json.RawMessage
	decodeAPIResponse(t, listed, &listedClients)
	if len(listedClients) != 1 {
		t.Fatalf("listed clients = %s", listed.Body.String())
	}
	assertPublicClientPayload(t, listedClients[0], "3.1")

	updated := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/public", `{"routing":{"mode":"full"}}`)
	assertAPIStatus(t, updated, http.StatusOK)
	assertPublicClientPayload(t, updated.Body.Bytes(), "3.1")

	regenerated := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients/public/regenerate-awg-params", "")
	assertAPIStatus(t, regenerated, http.StatusOK)
	assertPublicClientPayload(t, regenerated.Body.Bytes(), "3.1")

	lanGroup := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/lan-group", `{
		"client_ids":["public"],
		"lan_group_id":"peer:public"
	}`)
	assertAPIStatus(t, lanGroup, http.StatusOK)

	var lanGroupBody struct {
		Clients []json.RawMessage `json:"clients"`
	}
	decodeAPIResponse(t, lanGroup, &lanGroupBody)
	if len(lanGroupBody.Clients) != 1 {
		t.Fatalf("LAN group clients = %s", lanGroup.Body.String())
	}
	assertPublicClientPayload(t, lanGroupBody.Clients[0], "3.1")
}

func TestAPILANGroupFlow(t *testing.T) {
	handler, _, pool := newAuthorizedAPISmoke(t)

	primary := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"primary","lan_group_id":"peer:house"}`)
	assertAPIStatus(t, primary, http.StatusCreated)

	var explicitlyGrouped clientResponse
	decodeAPIResponse(t, primary, &explicitlyGrouped)
	if explicitlyGrouped.LANGroupID != "peer:house" {
		t.Fatalf("explicit LAN group = %q, want %q", explicitlyGrouped.LANGroupID, "peer:house")
	}

	device := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"device"}`)
	assertAPIStatus(t, device, http.StatusCreated)

	var defaulted clientResponse
	decodeAPIResponse(t, device, &defaulted)
	if defaulted.LANGroupID != "peer:device" {
		t.Fatalf("default LAN group = %q, want %q", defaulted.LANGroupID, "peer:device")
	}

	pool.firewallCalls = nil
	pool.firewallCallNum = 0

	updated := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/lan-group", `{
		"client_ids":["primary","device"],
		"lan_group_id":"peer:primary"
	}`)
	assertAPIStatus(t, updated, http.StatusOK)

	var body struct {
		Clients []clientResponse `json:"clients"`
	}
	decodeAPIResponse(t, updated, &body)
	if len(body.Clients) != 2 {
		t.Fatalf("updated clients = %+v", body.Clients)
	}
	for _, client := range body.Clients {
		if client.LANGroupID != "peer:primary" {
			t.Fatalf("updated client = %+v", client)
		}
	}

	var raw map[string][]map[string]json.RawMessage
	decodeAPIResponse(t, updated, &raw)
	for _, client := range raw["clients"] {
		for _, field := range []string{"private_key", "public_key", "preshared_key"} {
			if _, exposed := client[field]; exposed {
				t.Fatalf("LAN group response exposes %s", field)
			}
		}
	}

	if len(pool.firewallCalls) != 2 || len(pool.firewallCalls[0]) != 0 || len(pool.firewallCalls[1]) != 2 {
		t.Fatalf("firewall calls = %+v", pool.firewallCalls)
	}
}

func TestAPILANGroupValidatesBatchBeforeFirewallMutation(t *testing.T) {
	handler, _, pool := newAuthorizedAPISmoke(t)
	created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"primary"}`)
	assertAPIStatus(t, created, http.StatusCreated)
	pool.firewallCalls = nil
	pool.firewallCallNum = 0

	response := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/lan-group", `{
		"client_ids":["primary","missing"],
		"lan_group_id":"peer:primary"
	}`)
	assertAPIStatus(t, response, http.StatusNotFound)
	if len(pool.firewallCalls) != 0 {
		t.Fatalf("firewall calls = %+v, want none", pool.firewallCalls)
	}

	listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
	assertAPIStatus(t, listed, http.StatusOK)

	var clientsBody []clientResponse
	decodeAPIResponse(t, listed, &clientsBody)
	if len(clientsBody) != 1 || clientsBody[0].LANGroupID != "peer:primary" {
		t.Fatalf("clients after rejected batch = %+v", clientsBody)
	}
}

func TestAPILANGroupFirewallFailureIsFailClosed(t *testing.T) {
	handler, _, pool := newAuthorizedAPISmoke(t)
	for _, id := range []string{"primary", "device"} {
		created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"`+id+`"}`)
		assertAPIStatus(t, created, http.StatusCreated)
	}

	pool.firewallCalls = nil
	pool.firewallCallNum = 0
	pool.firewallErrAt = 2

	response := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/lan-group", `{
		"client_ids":["primary","device"],
		"lan_group_id":"peer:primary"
	}`)
	assertAPIStatus(t, response, http.StatusInternalServerError)
	if strings.Contains(response.Body.String(), "sensitive firewall details") {
		t.Fatalf("response exposes firewall error: %s", response.Body.String())
	}
	if len(pool.activeLANPeers) != 0 {
		t.Fatalf("active LAN peers = %+v, want fail-closed outage", pool.activeLANPeers)
	}

	listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
	assertAPIStatus(t, listed, http.StatusOK)

	var clientsBody []clientResponse
	decodeAPIResponse(t, listed, &clientsBody)
	for _, client := range clientsBody {
		if client.LANGroupID != "peer:primary" {
			t.Fatalf("committed client after firewall failure = %+v", client)
		}
	}
}

func TestAPILANGroupRejectsInvalidBodies(t *testing.T) {
	handler, _, _ := newAuthorizedAPISmoke(t)

	tests := []string{
		`{}`,
		`{"client_ids":[],"lan_group_id":"peer:primary"}`,
		`{"client_ids":["primary"],"lan_group_id":""}`,
		`{"client_ids":["primary","primary"],"lan_group_id":"peer:primary"}`,
		`{"client_ids":["primary"],"lan_group_id":"peer:primary"} {}`,
	}

	for _, body := range tests {
		response := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/lan-group", body)
		assertAPIStatus(t, response, http.StatusBadRequest)
	}
}

func TestAuthMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantStatus int
		wantCalled bool
	}{
		{name: "missing header", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", header: "Token " + testAPIToken, wantStatus: http.StatusUnauthorized},
		{name: "wrong token", header: "Bearer wrong-token", wantStatus: http.StatusUnauthorized},
		{name: "valid token", header: "Bearer " + testAPIToken, wantStatus: http.StatusNoContent, wantCalled: true},
	}

	server := &Server{config: &config.Config{APIToken: testAPIToken}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := server.authMiddleware(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			response := httptest.NewRecorder()

			if tt.header != "" {
				request.Header.Set("Authorization", tt.header)
			}

			handler(response, request)

			if response.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if called != tt.wantCalled {
				t.Errorf("next handler called = %t, want %t", called, tt.wantCalled)
			}
		})
	}
}

func TestClientUpdateErrorStatus(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "rollback failure", err: awg.ErrRollbackFailed, status: http.StatusInternalServerError},
		{name: "missing client", err: clients.ErrClientNotFound, status: http.StatusNotFound},
		{name: "invalid params", err: awg.ErrInvalidParams, status: http.StatusBadRequest},
		{name: "invalid port", err: awg.ErrInvalidPort, status: http.StatusBadRequest},
		{name: "invalid routing", err: clients.ErrInvalidRouting, status: http.StatusBadRequest},
		{name: "empty update", err: clients.ErrEmptyClientUpdate, status: http.StatusBadRequest},
		{name: "port in use", err: awg.ErrPortInUse, status: http.StatusConflict},
		{name: "shared port", err: awg.ErrPortShared, status: http.StatusConflict},
		{name: "profile port conflict", err: awg.ErrProfilePortConflict, status: http.StatusConflict},
		{name: "interface limit", err: awg.ErrMaxInterfacesReached, status: http.StatusServiceUnavailable},
		{name: "unknown error", err: errors.New("unknown"), status: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := fmt.Errorf("wrapped: %w", tt.err)
			if got := clientUpdateErrorStatus(wrapped); got != tt.status {
				t.Fatalf("clientUpdateErrorStatus() = %d, want %d", got, tt.status)
			}
		})
	}
}

func TestAPICreatePoolErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		status    int
		wantError string
	}{
		{
			name:      "internal error",
			err:       errors.New("sensitive device details"),
			status:    http.StatusInternalServerError,
			wantError: "internal server error",
		},
		{
			name:      "interface limit",
			err:       awg.ErrMaxInterfacesReached,
			status:    http.StatusServiceUnavailable,
			wantError: "add peer to device: " + awg.ErrMaxInterfacesReached.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _, pool := newAuthorizedAPISmoke(t)
			pool.addErr = tt.err

			response := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"failing-client"}`)
			assertAPIStatus(t, response, tt.status)

			var body map[string]string
			decodeAPIResponse(t, response, &body)
			if body["error"] != tt.wantError {
				t.Fatalf("error = %q, want %q", body["error"], tt.wantError)
			}
			if strings.Contains(response.Body.String(), "sensitive device details") {
				t.Fatalf("response exposes internal error: %s", response.Body.String())
			}
		})
	}
}

func TestAPIRejectsInvalidRequestBodies(t *testing.T) {
	handler, _, _ := newAuthorizedAPISmoke(t)
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		wantError string
	}{
		{
			name:      "malformed create",
			method:    http.MethodPost,
			path:      "/api/clients",
			body:      `{"id":`,
			wantError: "invalid request body",
		},
		{
			name:      "trailing JSON create",
			method:    http.MethodPost,
			path:      "/api/clients",
			body:      `{"id":"one"} {"id":"two"}`,
			wantError: "invalid request body",
		},
		{
			name:      "oversized create",
			method:    http.MethodPost,
			path:      "/api/clients",
			body:      `{"id":"oversized","padding":"` + strings.Repeat("x", 1<<20) + `"}`,
			wantError: "invalid request body",
		},
		{
			name:      "empty PATCH",
			method:    http.MethodPatch,
			path:      "/api/clients/missing",
			body:      `{}`,
			wantError: clients.ErrEmptyClientUpdate.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := authorizedAPIRequest(t, handler, tt.method, tt.path, tt.body)
			assertAPIStatus(t, response, http.StatusBadRequest)

			var body map[string]string
			decodeAPIResponse(t, response, &body)
			if body["error"] != tt.wantError {
				t.Fatalf("error = %q, want %q", body["error"], tt.wantError)
			}
		})
	}
}

func TestRegenerationRequiresUsageSnapshot(t *testing.T) {
	handler, _, pool := newAuthorizedAPISmoke(t)
	created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"snapshot-client"}`)
	assertAPIStatus(t, created, http.StatusCreated)

	profileKey := pool.profileKey
	if profileKey == (awg.ProfileKey{}) {
		t.Fatal("profile key missing before regeneration")
	}
	pool.dumpErr = errors.New("sensitive dump details")

	response := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients/snapshot-client/regenerate-awg-params", "")
	assertAPIStatus(t, response, http.StatusInternalServerError)

	var body map[string]string
	decodeAPIResponse(t, response, &body)
	if body["error"] != "internal server error" {
		t.Fatalf("error = %q, want generic message", body["error"])
	}
	if strings.Contains(response.Body.String(), "sensitive dump details") {
		t.Fatalf("response exposes snapshot error: %s", response.Body.String())
	}
	if pool.migrations != 0 {
		t.Fatalf("migrations = %d, want 0", pool.migrations)
	}
	if pool.profileKey != profileKey {
		t.Fatalf("profile key changed after snapshot failure")
	}
}

func TestAPIOperationFailures(t *testing.T) {
	t.Run("failed DELETE preserves client and peer", func(t *testing.T) {
		handler, _, pool := newAuthorizedAPISmoke(t)
		created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"delete-client"}`)
		assertAPIStatus(t, created, http.StatusCreated)
		pool.removeErr = errors.New("sensitive remove details")

		response := authorizedAPIRequest(t, handler, http.MethodDelete, "/api/clients/delete-client", "")
		assertAPIStatus(t, response, http.StatusInternalServerError)

		var body map[string]string
		decodeAPIResponse(t, response, &body)
		if body["error"] != "internal server error" || strings.Contains(response.Body.String(), "sensitive remove details") {
			t.Fatalf("delete error response = %s", response.Body.String())
		}
		if !pool.hasPeer {
			t.Fatal("peer removed after failed DELETE")
		}

		listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
		assertAPIStatus(t, listed, http.StatusOK)

		var remaining []clientResponse
		decodeAPIResponse(t, listed, &remaining)
		if len(remaining) != 1 || remaining[0].ID != "delete-client" {
			t.Fatalf("clients after failed DELETE = %+v", remaining)
		}
	})

	t.Run("shared-port PATCH preserves client and peer", func(t *testing.T) {
		handler, _, pool := newAuthorizedAPISmoke(t)
		created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"shared-client"}`)
		assertAPIStatus(t, created, http.StatusCreated)
		profileKey := pool.profileKey
		peerPort := pool.peerPort
		pool.migrateErr = awg.ErrPortShared

		response := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/shared-client", `{"awg_params":{"port":51830}}`)
		assertAPIStatus(t, response, http.StatusConflict)
		if pool.migrations != 0 || pool.profileKey != profileKey || pool.peerPort != peerPort {
			t.Fatalf("peer changed after failed PATCH: migrations=%d profile=%v port=%d", pool.migrations, pool.profileKey, pool.peerPort)
		}

		listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
		assertAPIStatus(t, listed, http.StatusOK)

		var clientsBody []clientResponse
		decodeAPIResponse(t, listed, &clientsBody)
		if len(clientsBody) != 1 || clientsBody[0].AWGParams != nil {
			t.Fatalf("client changed after failed PATCH: %+v", clientsBody)
		}
	})

	t.Run("configuration hides port lookup error", func(t *testing.T) {
		handler, _, pool := newAuthorizedAPISmoke(t)
		created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{"id":"configuration-client"}`)
		assertAPIStatus(t, created, http.StatusCreated)
		pool.portErr = errors.New("sensitive configuration details")

		response := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients/configuration-client/configuration", "")
		assertAPIStatus(t, response, http.StatusInternalServerError)

		var body map[string]string
		decodeAPIResponse(t, response, &body)
		if body["error"] != "internal server error" || strings.Contains(response.Body.String(), "sensitive configuration details") {
			t.Fatalf("configuration error response = %s", response.Body.String())
		}
	})
}

func TestAuthorizedAPIFlow(t *testing.T) {
	handler, collector, pool := newAuthorizedAPISmoke(t)

	healthRequest := httptest.NewRequest(http.MethodGet, "/health", nil)
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, healthRequest)
	assertAPIStatus(t, health, http.StatusOK)

	generated := authorizedAPIRequest(t, handler, http.MethodPost, "/api/awg-params/generate", "")
	assertAPIStatus(t, generated, http.StatusOK)

	var generatedParams awg.GeneratedParams
	decodeAPIResponse(t, generated, &generatedParams)
	if generatedParams.H1 == "" || generatedParams.H4 == "" {
		t.Fatalf("generated params = %+v", generatedParams)
	}

	listed := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients", "")
	assertAPIStatus(t, listed, http.StatusOK)

	var initialClients []clientResponse
	decodeAPIResponse(t, listed, &initialClients)
	if len(initialClients) != 0 {
		t.Fatalf("initial client count = %d, want 0", len(initialClients))
	}

	created := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients", `{
		"id":"smoke-client",
		"awg_params":{"client_listen_port":51830,"mtu":1380,"dns_mode":"custom","dns_servers":["9.9.9.9"]},
		"routing":{"mode":"split","allowed_ips":["10.1.2.3/8"]}
	}`)
	assertAPIStatus(t, created, http.StatusCreated)
	if contentType := created.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("create Content-Type = %q", contentType)
	}

	var createdClient clientResponse
	decodeAPIResponse(t, created, &createdClient)
	if createdClient.ID != "smoke-client" || createdClient.Address != "10.77.0.2" {
		t.Fatalf("created client = %+v", createdClient)
	}
	if createdClient.AWGParams == nil || createdClient.AWGParams.MTU != 1380 {
		t.Fatalf("created AWG params = %+v", createdClient.AWGParams)
	}
	if createdClient.Routing.Mode != clients.RoutingModeSplit ||
		len(createdClient.Routing.AllowedIPs) != 1 ||
		createdClient.Routing.AllowedIPs[0] != "10.0.0.0/8" {
		t.Fatalf("created routing = %+v", createdClient.Routing)
	}

	var createdJSON map[string]json.RawMessage
	decodeAPIResponse(t, created, &createdJSON)
	for _, field := range []string{"private_key", "public_key", "preshared_key"} {
		if _, ok := createdJSON[field]; ok {
			t.Fatalf("create response exposes %s", field)
		}
	}

	collector.Collect()
	usagePublicKey := awg.KeyToBase64(pool.peerPublicKey)
	if _, ok := collector.GetStats(usagePublicKey); !ok {
		t.Fatal("usage stats missing before DELETE")
	}

	stats := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients/smoke-client/stats", "")
	assertAPIStatus(t, stats, http.StatusOK)

	var statsBody statsResponse
	decodeAPIResponse(t, stats, &statsBody)
	if statsBody.RxBytes != 1024 || statsBody.TxBytes != 2048 {
		t.Fatalf("stats = %+v", statsBody)
	}

	splitConfiguration := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients/smoke-client/configuration", "")
	assertAPIStatus(t, splitConfiguration, http.StatusOK)
	if !strings.Contains(splitConfiguration.Body.String(), "AllowedIPs = 10.77.0.0/24, 10.0.0.0/8") {
		t.Fatalf("split configuration:\n%s", splitConfiguration.Body.String())
	}

	updated := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/smoke-client", `{"routing":null}`)
	assertAPIStatus(t, updated, http.StatusOK)

	var updatedClient clientResponse
	decodeAPIResponse(t, updated, &updatedClient)
	if updatedClient.AWGParams == nil || updatedClient.AWGParams.MTU != 1380 {
		t.Fatalf("PATCH did not preserve omitted awg_params: %+v", updatedClient.AWGParams)
	}
	if updatedClient.Routing.Mode != clients.RoutingModeFull {
		t.Fatalf("PATCH routing reset = %+v", updatedClient.Routing)
	}
	if pool.migrations != 0 {
		t.Fatalf("routing-only PATCH migrations = %d, want 0", pool.migrations)
	}

	configuration := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients/smoke-client/configuration", "")
	assertAPIStatus(t, configuration, http.StatusOK)
	if contentType := configuration.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("configuration Content-Type = %q", contentType)
	}
	for _, line := range []string{
		"ListenPort = 51830",
		"DNS = 9.9.9.9",
		"MTU = 1380",
		"Endpoint = vpn.example.test:51820",
		"AllowedIPs = 10.77.0.0/24, 0.0.0.0/0, ::/0",
	} {
		if !strings.Contains(configuration.Body.String(), line) {
			t.Fatalf("configuration does not contain %q:\n%s", line, configuration.Body.String())
		}
	}

	presharedKey := ""
	for _, line := range strings.Split(configuration.Body.String(), "\n") {
		if value, ok := strings.CutPrefix(line, "PresharedKey = "); ok {
			presharedKey = value
			break
		}
	}
	if _, err := awg.Base64ToKey(presharedKey); err != nil {
		t.Fatalf("configuration PresharedKey = %q: %v", presharedKey, err)
	}

	regenerated := authorizedAPIRequest(t, handler, http.MethodPost, "/api/clients/smoke-client/regenerate-awg-params", "")
	assertAPIStatus(t, regenerated, http.StatusOK)

	var regeneratedClient clientResponse
	decodeAPIResponse(t, regenerated, &regeneratedClient)
	if regeneratedClient.ID != "smoke-client" || regeneratedClient.AWGParams == nil {
		t.Fatalf("regenerated client = %+v", regeneratedClient)
	}
	if pool.migrations != 1 {
		t.Fatalf("regeneration migrations = %d, want 1", pool.migrations)
	}

	reset := authorizedAPIRequest(t, handler, http.MethodPatch, "/api/clients/smoke-client", `{"awg_params":null}`)
	assertAPIStatus(t, reset, http.StatusOK)

	var resetClient clientResponse
	decodeAPIResponse(t, reset, &resetClient)
	if resetClient.AWGParams != nil || resetClient.Routing.Mode != clients.RoutingModeFull {
		t.Fatalf("PATCH reset = %+v", resetClient)
	}
	if pool.migrations != 2 {
		t.Fatalf("AWG reset migrations = %d, want 2", pool.migrations)
	}

	deleted := authorizedAPIRequest(t, handler, http.MethodDelete, "/api/clients/smoke-client", "")
	assertAPIStatus(t, deleted, http.StatusNoContent)
	if deleted.Body.Len() != 0 {
		t.Fatalf("delete body = %q, want empty", deleted.Body.String())
	}
	if pool.hasPeer {
		t.Fatal("peer remains after DELETE")
	}
	if _, ok := collector.GetStats(usagePublicKey); ok {
		t.Fatal("usage stats remain after DELETE")
	}

	missing := authorizedAPIRequest(t, handler, http.MethodGet, "/api/clients/smoke-client/configuration", "")
	assertAPIStatus(t, missing, http.StatusNotFound)

	var missingBody map[string]string
	decodeAPIResponse(t, missing, &missingBody)
	if missingBody["error"] != clients.ErrClientNotFound.Error() {
		t.Fatalf("missing client error = %q", missingBody["error"])
	}
}

func newAuthorizedAPISmoke(t *testing.T) (http.Handler, *usage.Collector, *apiSmokePool) {
	handler, collector, pool, _ := newAuthorizedAPISmokeWithStorage(t)

	return handler, collector, pool
}

func newAuthorizedAPISmokeWithStorage(t *testing.T) (http.Handler, *usage.Collector, *apiSmokePool, *clients.Storage) {
	t.Helper()

	dataDir := t.TempDir()
	cfg := &config.Config{
		APIToken:               testAPIToken,
		Address:                "10.77.0.1/24",
		Endpoint:               "vpn.example.test",
		ListenPort:             51820,
		HTTPPort:               7777,
		MTU:                    1420,
		DNS:                    "1.1.1.1",
		DataDir:                dataDir,
		DefaultProtocolVersion: "3.1",
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
	pool := &apiSmokePool{serverPublicKey: [32]byte{1}}
	storage := clients.NewStorage(dataDir)
	data := &clients.StorageData{AWG31: apiAWG31Storage()}
	if err := storage.Save(data); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	plan, err := clients.PrepareRestorePlan(cfg, clients.ManagerDefaults{
		LegacyParams:   defaultParams,
		AWG31Params:    apiAWG31Defaults(t),
		DefaultVersion: awg.ProtocolVersion31,
	}, data)
	if err != nil {
		t.Fatalf("PrepareRestorePlan() error = %v", err)
	}
	manager, err := clients.NewManagerFromRestorePlan(pool, storage, cfg, plan)
	if err != nil {
		t.Fatalf("NewManagerFromRestorePlan() error = %v", err)
	}

	collector := usage.NewCollector(dataDir, pool.interfaceNames, pool.showDump)
	server := NewServer(manager, cfg, collector)

	return server.httpServer.Handler, collector, pool, storage
}

func authorizedAPIRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+testAPIToken)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func assertAPIStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()

	if response.Code != want {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, want, response.Body.String())
	}
}

func decodeAPIResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()

	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func assertPublicClientPayload(t *testing.T, payload []byte, wantVersion string) {
	t.Helper()

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode client payload %q: %v", payload, err)
	}

	encodedVersion, exists := fields["protocol_version"]
	if !exists {
		t.Fatalf("client payload omits protocol_version: %s", payload)
	}

	var version string
	if err := json.Unmarshal(encodedVersion, &version); err != nil {
		t.Fatalf("decode protocol_version %q: %v", encodedVersion, err)
	}
	if version != wantVersion {
		t.Fatalf("protocol_version = %q, want %q", version, wantVersion)
	}

	for _, field := range []string{
		"header_key_id",
		"header_protection_key",
		"private_key",
		"public_key",
		"preshared_key",
		"server_private_key",
	} {
		if _, exposed := fields[field]; exposed {
			t.Fatalf("client payload exposes %s: %s", field, payload)
		}
	}

	secret := apiAWG31Storage().HeaderKeys["api-synthetic-default-id"].HeaderProtectionKey
	if bytes.Contains(payload, []byte(secret)) {
		t.Fatalf("client payload exposes header protection key: %s", payload)
	}
}

func assertGeneratedParamsForProtocol(t *testing.T, payload []byte, version string) {
	t.Helper()

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode generated params %q: %v", payload, err)
	}

	for _, field := range []string{"h1", "h2", "h3", "h4", "s1", "s2"} {
		if _, exists := fields[field]; !exists {
			t.Fatalf("generated params omit %s: %s", field, payload)
		}
	}

	var h1 string
	if err := json.Unmarshal(fields["h1"], &h1); err != nil {
		t.Fatalf("decode generated h1 %q: %v", fields["h1"], err)
	}

	switch version {
	case "2.0":
		if _, exists := fields["s3"]; exists {
			t.Fatalf("legacy generated params expose s3: %s", payload)
		}
		if _, exists := fields["s4"]; exists {
			t.Fatalf("legacy generated params expose s4: %s", payload)
		}
		if !strings.Contains(h1, "-") {
			t.Fatalf("legacy generated h1 = %q, want range", h1)
		}
	case "3.1":
		for _, field := range []string{"s3", "s4"} {
			if _, exists := fields[field]; !exists {
				t.Fatalf("AWG 3.1 generated params omit %s: %s", field, payload)
			}
		}
		if strings.Contains(h1, "-") {
			t.Fatalf("AWG 3.1 generated h1 = %q, want fixed value", h1)
		}
	default:
		t.Fatalf("unsupported expected protocol version %q", version)
	}

	for _, field := range []string{
		"header_key_id",
		"header_protection_key",
		"private_key",
		"preshared_key",
	} {
		if _, exposed := fields[field]; exposed {
			t.Fatalf("generated params expose %s: %s", field, payload)
		}
	}
}

func apiAWG31Storage() *clients.AWG31Storage {
	return &clients.AWG31Storage{
		DefaultHeaderKeyID: "api-synthetic-default-id",
		GeneratedParams: &awg.GeneratedParamsV31{
			H1: "100001", H2: "1000001", H3: "10000001", H4: "100000001",
			S1: 15, S2: 72, S3: 15, S4: 12,
		},
		HeaderKeys: map[string]clients.HeaderKeyData{
			"api-synthetic-default-id": {HeaderProtectionKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xa5}, 32))},
		},
	}
}

func apiAWG31Defaults(t *testing.T) awg.AWGParams {
	t.Helper()

	parseRange := func(value string) *config.Uint16Range {
		parsed, err := config.ParseUint16Range(value)
		if err != nil {
			t.Fatalf("ParseUint16Range(%q) error = %v", value, err)
		}

		return &parsed
	}

	return awg.AWGParams{
		MTU:                    1280,
		DNS:                    "1.1.1.1",
		Jc:                     5,
		Jmin:                   50,
		Jmax:                   1000,
		PersistentKeepalive:    parseRange("25-35"),
		ContentPaddingAddition: parseRange("10-100"),
		RekeyAfterTime:         parseRange("100-120"),
		RekeyTimeout:           parseRange("3-7"),
		RejectAfterTime:        parseRange("150-180"),
		KeepaliveTimeout:       parseRange("5-15"),
		MaxHandshakeAttempts:   parseRange("15-20"),
		RandomTrailers:         "on",
		DisableCookies:         "off",
	}
}
