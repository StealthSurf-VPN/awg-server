package clients

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
)

type ClientData struct {
	ID         string `json:"id"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	Address    string         `json:"address"`
	CreatedAt  string         `json:"created_at"`
	AWGParams  *awg.AWGParams `json:"awg_params,omitempty"`
}

type StorageData struct {
	ServerPrivateKey string       `json:"server_private_key"`
	Clients          []ClientData `json:"clients"`
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
