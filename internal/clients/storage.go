package clients

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

type ClientData struct {
	ID              string              `json:"id"`
	ProtocolVersion awg.ProtocolVersion `json:"protocol_version,omitempty"`
	PrivateKey      string              `json:"private_key"`
	PublicKey       string              `json:"public_key"`
	PresharedKey    string              `json:"preshared_key,omitempty"`
	Address         string              `json:"address"`
	LANGroupID      string              `json:"lan_group_id"`
	CreatedAt       string              `json:"created_at"`
	AWGParams       *awg.AWGParams      `json:"awg_params,omitempty"`
	Routing         *Routing            `json:"routing,omitempty"`

	headerKeyID string
}

type HeaderKeyData struct {
	HeaderProtectionKey string `json:"header_protection_key"`
}

type AWG31Storage struct {
	DefaultHeaderKeyID string                   `json:"default_header_key_id"`
	GeneratedParams    *awg.GeneratedParamsV31  `json:"generated_params"`
	HeaderKeys         map[string]HeaderKeyData `json:"header_keys"`
}

type StorageData struct {
	ServerPrivateKey string               `json:"server_private_key"`
	GeneratedParams  *awg.GeneratedParams `json:"generated_params,omitempty"`
	Clients          []ClientData         `json:"clients"`
	AWG31            *AWG31Storage        `json:"awg_31,omitempty"`

	needsNormalization bool
}

type storageDataDisk struct {
	ServerPrivateKey string               `json:"server_private_key"`
	GeneratedParams  *awg.GeneratedParams `json:"generated_params,omitempty"`
	Clients          []clientDataDisk     `json:"clients"`
	AWG31            *AWG31Storage        `json:"awg_31,omitempty"`
}

type clientDataDisk struct {
	ID              string         `json:"id"`
	ProtocolVersion string         `json:"protocol_version,omitempty"`
	PrivateKey      string         `json:"private_key"`
	PublicKey       string         `json:"public_key"`
	PresharedKey    string         `json:"preshared_key,omitempty"`
	Address         string         `json:"address"`
	LANGroupID      string         `json:"lan_group_id"`
	CreatedAt       string         `json:"created_at"`
	AWGParams       *awg.AWGParams `json:"awg_params,omitempty"`
	Routing         *Routing       `json:"routing,omitempty"`
	HeaderKeyID     string         `json:"header_key_id,omitempty"`
}

type Storage struct {
	filePath string
}

func NewStorage(dataDir string) *Storage {
	return &Storage{
		filePath: filepath.Join(dataDir, "clients.json"),
	}
}

func (s *Storage) Load() (*StorageData, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &StorageData{}, nil
		}

		return nil, fmt.Errorf("read storage file: %w", err)
	}

	var storageData StorageData

	if err := json.Unmarshal(data, &storageData); err != nil {
		return nil, fmt.Errorf("parse storage file: %w", err)
	}

	return &storageData, nil
}

func (s *Storage) Save(data *StorageData) error {
	dir := filepath.Dir(s.filePath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal storage data: %w", err)
	}

	tmpPath := s.filePath + ".tmp"

	if err := os.WriteFile(tmpPath, b, 0600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.filePath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

func PrepareStorageDefaults(data *StorageData) (*StorageData, error) {
	if data == nil {
		return nil, errors.New("storage data is required")
	}
	if len(data.Clients) > 0 && (data.ServerPrivateKey == "" || data.GeneratedParams == nil) {
		return nil, errors.New("persisted clients require server_private_key and generated_params")
	}

	prepared := cloneStorageData(data)

	if prepared.ServerPrivateKey == "" {
		privateKey, err := awg.GeneratePrivateKey()
		if err != nil {
			return nil, fmt.Errorf("generate server private key: %w", err)
		}

		prepared.ServerPrivateKey = awg.KeyToBase64(privateKey)
		prepared.needsNormalization = true
	}

	if prepared.GeneratedParams == nil {
		generated, err := awg.GenerateParams()
		if err != nil {
			return nil, fmt.Errorf("generate AWG params: %w", err)
		}

		prepared.GeneratedParams = generated
		prepared.needsNormalization = true
	}

	return prepared, nil
}

func (data StorageData) MarshalJSON() ([]byte, error) {
	disk := storageDataDisk{
		ServerPrivateKey: data.ServerPrivateKey,
		GeneratedParams:  data.GeneratedParams,
		Clients:          make([]clientDataDisk, 0, len(data.Clients)),
		AWG31:            data.AWG31,
	}

	for _, client := range data.Clients {
		disk.Clients = append(disk.Clients, clientDataDisk{
			ID:              client.ID,
			ProtocolVersion: string(client.ProtocolVersion),
			PrivateKey:      client.PrivateKey,
			PublicKey:       client.PublicKey,
			PresharedKey:    client.PresharedKey,
			Address:         client.Address,
			LANGroupID:      client.LANGroupID,
			CreatedAt:       client.CreatedAt,
			AWGParams:       client.AWGParams,
			Routing:         client.Routing,
			HeaderKeyID:     client.headerKeyID,
		})
	}

	return json.Marshal(disk)
}

func (data *StorageData) UnmarshalJSON(encoded []byte) error {
	var raw struct {
		ServerPrivateKey string               `json:"server_private_key"`
		GeneratedParams  *awg.GeneratedParams `json:"generated_params,omitempty"`
		Clients          []json.RawMessage    `json:"clients"`
		AWG31            *AWG31Storage        `json:"awg_31,omitempty"`
	}

	if err := json.Unmarshal(encoded, &raw); err != nil {
		return err
	}

	clients := make([]ClientData, 0, len(raw.Clients))
	needsNormalization := false
	for index, encodedClient := range raw.Clients {
		client, err := decodeStoredClient(encodedClient)
		if err != nil {
			return fmt.Errorf("decode client %d: %w", index, err)
		}
		if storedRangesNeedNormalization(client.AWGParams) {
			needsNormalization = true
		}

		clients = append(clients, client)
	}

	*data = StorageData{
		ServerPrivateKey:   raw.ServerPrivateKey,
		GeneratedParams:    raw.GeneratedParams,
		Clients:            clients,
		AWG31:              raw.AWG31,
		needsNormalization: needsNormalization,
	}

	return nil
}

func storedRangesNeedNormalization(params *awg.AWGParams) bool {
	if params == nil {
		return false
	}

	for _, value := range []*config.Uint16Range{
		params.PersistentKeepalive,
		params.ContentPaddingAddition,
		params.RekeyAfterTime,
		params.RekeyTimeout,
		params.RejectAfterTime,
		params.KeepaliveTimeout,
		params.MaxHandshakeAttempts,
	} {
		if value != nil && !value.IsCanonical() {
			return true
		}
	}

	return false
}

func decodeStoredClient(encoded []byte) (ClientData, error) {
	var disk clientDataDisk
	if err := json.Unmarshal(encoded, &disk); err != nil {
		return ClientData{}, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return ClientData{}, err
	}

	version, err := decodePersistedProtocolVersion(fields)
	if err != nil {
		return ClientData{}, err
	}

	headerKeyID, err := decodeOptionalStoredString(fields, "header_key_id")
	if err != nil {
		return ClientData{}, err
	}

	return ClientData{
		ID:              disk.ID,
		ProtocolVersion: version,
		PrivateKey:      disk.PrivateKey,
		PublicKey:       disk.PublicKey,
		PresharedKey:    disk.PresharedKey,
		Address:         disk.Address,
		LANGroupID:      disk.LANGroupID,
		CreatedAt:       disk.CreatedAt,
		AWGParams:       disk.AWGParams,
		Routing:         disk.Routing,
		headerKeyID:     headerKeyID,
	}, nil
}

func decodePersistedProtocolVersion(fields map[string]json.RawMessage) (awg.ProtocolVersion, error) {
	encoded, exists, err := storedJSONField(fields, "protocol_version")
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	if bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return "", fmt.Errorf("protocol_version must be the canonical string 2.0 or 3.1")
	}

	var version string
	if err := json.Unmarshal(encoded, &version); err != nil {
		return "", fmt.Errorf("decode protocol_version: %w", err)
	}

	switch awg.ProtocolVersion(version) {
	case awg.ProtocolVersion2, awg.ProtocolVersion31:
		return awg.ProtocolVersion(version), nil
	default:
		return "", fmt.Errorf("protocol_version must be the canonical string 2.0 or 3.1")
	}
}

func decodeOptionalStoredString(fields map[string]json.RawMessage, name string) (string, error) {
	encoded, exists, err := storedJSONField(fields, name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	if bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return "", fmt.Errorf("%s must be a string", name)
	}

	var value string
	if err := json.Unmarshal(encoded, &value); err != nil {
		return "", fmt.Errorf("decode %s: %w", name, err)
	}

	return value, nil
}

func storedJSONField(fields map[string]json.RawMessage, name string) (json.RawMessage, bool, error) {
	encoded, exists := fields[name]
	for field := range fields {
		if field != name && strings.EqualFold(field, name) {
			return nil, false, fmt.Errorf("%s must use the exact lower-case JSON field name", name)
		}
	}

	return encoded, exists, nil
}

func cloneStorageData(data *StorageData) *StorageData {
	if data == nil {
		return nil
	}

	clone := *data
	clone.GeneratedParams = cloneGeneratedParams(data.GeneratedParams)
	clone.Clients = make([]ClientData, len(data.Clients))
	for index, client := range data.Clients {
		clone.Clients[index] = cloneClientData(client)
	}
	clone.AWG31 = cloneAWG31Storage(data.AWG31)

	return &clone
}

func cloneClientData(client ClientData) ClientData {
	clone := client
	clone.AWGParams = cloneStoredAWGParams(client.AWGParams)
	clone.Routing = cloneRouting(client.Routing)

	return clone
}

func cloneGeneratedParams(params *awg.GeneratedParams) *awg.GeneratedParams {
	if params == nil {
		return nil
	}

	clone := *params

	return &clone
}

func cloneAWG31Storage(storage *AWG31Storage) *AWG31Storage {
	if storage == nil {
		return nil
	}

	clone := *storage
	if storage.GeneratedParams != nil {
		generated := *storage.GeneratedParams
		clone.GeneratedParams = &generated
	}
	if storage.HeaderKeys != nil {
		clone.HeaderKeys = make(map[string]HeaderKeyData, len(storage.HeaderKeys))
		for id, key := range storage.HeaderKeys {
			clone.HeaderKeys[id] = key
		}
	}

	return &clone
}

func cloneStoredAWGParams(params *awg.AWGParams) *awg.AWGParams {
	if params == nil {
		return nil
	}

	clone := *params
	clone.DNSServers = append([]string(nil), params.DNSServers...)
	clone.PersistentKeepalive = cloneStoredRange(params.PersistentKeepalive)
	clone.ContentPaddingAddition = cloneStoredRange(params.ContentPaddingAddition)
	clone.RekeyAfterTime = cloneStoredRange(params.RekeyAfterTime)
	clone.RekeyTimeout = cloneStoredRange(params.RekeyTimeout)
	clone.RejectAfterTime = cloneStoredRange(params.RejectAfterTime)
	clone.KeepaliveTimeout = cloneStoredRange(params.KeepaliveTimeout)
	clone.MaxHandshakeAttempts = cloneStoredRange(params.MaxHandshakeAttempts)

	return &clone
}

func cloneStoredRange(value *config.Uint16Range) *config.Uint16Range {
	if value == nil {
		return nil
	}

	clone := *value

	return &clone
}

func cloneRouting(routing *Routing) *Routing {
	if routing == nil {
		return nil
	}

	clone := *routing
	clone.AllowedIPs = append([]string(nil), routing.AllowedIPs...)
	clone.ExcludedIPs = append([]string(nil), routing.ExcludedIPs...)

	return &clone
}

func persistedValueChanged(before, after any) (bool, error) {
	encodedBefore, err := json.Marshal(before)
	if err != nil {
		return false, fmt.Errorf("marshal persisted value before normalization: %w", err)
	}
	encodedAfter, err := json.Marshal(after)
	if err != nil {
		return false, fmt.Errorf("marshal persisted value after normalization: %w", err)
	}

	return !bytes.Equal(encodedBefore, encodedAfter), nil
}

func generateHeaderKeyID() (string, error) {
	var randomBytes [16]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", fmt.Errorf("generate header key ID: %w", err)
	}

	return hex.EncodeToString(randomBytes[:]), nil
}
