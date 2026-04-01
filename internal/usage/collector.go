package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
)

type PeerStats struct {
	TotalRx       int64     `json:"total_rx"`
	TotalTx       int64     `json:"total_tx"`
	LastRawRx     int64     `json:"last_raw_rx"`
	LastRawTx     int64     `json:"last_raw_tx"`
	LastHandshake time.Time `json:"last_handshake,omitempty"`
}

type Collector struct {
	mu       sync.RWMutex
	filePath string
	stats    map[string]*PeerStats
	ifacesFn func() []string
	dumpFn   func(string) ([]awg.PeerDump, error)
}

func NewCollector(dataDir string, ifacesFn func() []string, dumpFn func(string) ([]awg.PeerDump, error)) *Collector {
	c := &Collector{
		filePath: filepath.Join(dataDir, "usage.json"),
		stats:    make(map[string]*PeerStats),
		ifacesFn: ifacesFn,
		dumpFn:   dumpFn,
	}

	if err := c.load(); err != nil {
		log.Printf("warning: failed to load usage data: %v", err)
	}

	return c
}

func (c *Collector) Run(ctx context.Context) {
	c.Collect()

	if err := c.Save(); err != nil {
		log.Printf("warning: failed to save initial usage data: %v", err)
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Collect()

			if err := c.Save(); err != nil {
				log.Printf("warning: failed to save usage data: %v", err)
			}
		}
	}
}

func (c *Collector) Collect() {
	ifaces := c.ifacesFn()

	var allPeers []awg.PeerDump

	for _, ifName := range ifaces {
		peers, err := c.dumpFn(ifName)
		if err != nil {
			log.Printf("warning: failed to dump interface %s: %v", ifName, err)
			continue
		}

		allPeers = append(allPeers, peers...)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, p := range allPeers {
		stats, ok := c.stats[p.PublicKey]
		if !ok {
			stats = &PeerStats{
				TotalRx:   p.TransferRx,
				TotalTx:   p.TransferTx,
				LastRawRx: p.TransferRx,
				LastRawTx: p.TransferTx,
			}

			c.stats[p.PublicKey] = stats
		} else {
			var deltaRx, deltaTx int64

			if p.TransferRx < stats.LastRawRx {
				deltaRx = p.TransferRx
			} else {
				deltaRx = p.TransferRx - stats.LastRawRx
			}

			if p.TransferTx < stats.LastRawTx {
				deltaTx = p.TransferTx
			} else {
				deltaTx = p.TransferTx - stats.LastRawTx
			}

			stats.TotalRx += deltaRx
			stats.TotalTx += deltaTx
			stats.LastRawRx = p.TransferRx
			stats.LastRawTx = p.TransferTx
		}

		if !p.LastHandshake.IsZero() {
			stats.LastHandshake = p.LastHandshake
		}
	}
}

func (c *Collector) GetStats(publicKey string) (PeerStats, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats, ok := c.stats[publicKey]
	if !ok {
		return PeerStats{}, false
	}

	return *stats, true
}

func (c *Collector) RemoveStats(publicKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.stats, publicKey)
}

func (c *Collector) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	b, err := json.MarshalIndent(c.stats, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal usage data: %w", err)
	}

	tmpPath := c.filePath + ".tmp"

	if err := os.WriteFile(tmpPath, b, 0600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmpPath, c.filePath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

func (c *Collector) load() error {
	data, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("read usage file: %w", err)
	}

	if err := json.Unmarshal(data, &c.stats); err != nil {
		return fmt.Errorf("parse usage file: %w", err)
	}

	return nil
}
