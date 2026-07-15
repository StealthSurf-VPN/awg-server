package usage

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
)

func TestCollectorAccumulatesDeltasAndCounterResets(t *testing.T) {
	firstHandshake := time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)
	secondHandshake := firstHandshake.Add(time.Minute)
	samples := [][]awg.PeerDump{
		{{PublicKey: "peer", TransferRx: 100, TransferTx: 200, LastHandshake: firstHandshake}},
		{{PublicKey: "peer", TransferRx: 150, TransferTx: 260, LastHandshake: secondHandshake}},
		{{PublicKey: "peer", TransferRx: 10, TransferTx: 20}},
	}
	call := 0
	collector := NewCollector(
		t.TempDir(),
		func() []string { return []string{"awg0"} },
		func(string) ([]awg.PeerDump, error) {
			peers := samples[call]
			call++
			return peers, nil
		},
	)

	wants := []PeerStats{
		{TotalRx: 100, TotalTx: 200, LastRawRx: 100, LastRawTx: 200, LastHandshake: firstHandshake},
		{TotalRx: 150, TotalTx: 260, LastRawRx: 150, LastRawTx: 260, LastHandshake: secondHandshake},
		{TotalRx: 160, TotalTx: 280, LastRawRx: 10, LastRawTx: 20, LastHandshake: secondHandshake},
	}

	for index, want := range wants {
		collector.Collect()
		got, ok := collector.GetStats("peer")
		if !ok {
			t.Fatalf("collection %d did not create peer stats", index+1)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("collection %d stats = %+v, want %+v", index+1, got, want)
		}
	}
}

func TestCollectorRequiredSnapshot(t *testing.T) {
	actionErr := errors.New("action failed")
	tests := []struct {
		name       string
		peers      []awg.PeerDump
		dumpErr    error
		actionErr  error
		wantErr    error
		wantAction bool
	}{
		{name: "dump failure", dumpErr: errors.New("dump failed"), wantErr: ErrSnapshotFailed},
		{name: "empty active interface", wantErr: ErrSnapshotFailed},
		{
			name:       "successful snapshot runs action",
			peers:      []awg.PeerDump{{PublicKey: "peer", TransferRx: 10, TransferTx: 20}},
			actionErr:  actionErr,
			wantErr:    actionErr,
			wantAction: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := NewCollector(
				t.TempDir(),
				func() []string { return []string{"awg0"} },
				func(string) ([]awg.PeerDump, error) { return tt.peers, tt.dumpErr },
			)
			actionCalled := false

			err := collector.WithRequiredSnapshot(func() error {
				actionCalled = true
				return tt.actionErr
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("WithRequiredSnapshot() error = %v, want %v", err, tt.wantErr)
			}
			if actionCalled != tt.wantAction {
				t.Fatalf("action called = %t, want %t", actionCalled, tt.wantAction)
			}
		})
	}
}

func TestCollectorRequiredSnapshotDoesNotCommitPartialCounters(t *testing.T) {
	collector := NewCollector(
		t.TempDir(),
		func() []string { return []string{"awg0", "awg1"} },
		func(ifName string) ([]awg.PeerDump, error) {
			if ifName == "awg0" {
				return []awg.PeerDump{{PublicKey: "peer", TransferRx: 100, TransferTx: 200}}, nil
			}
			return nil, errors.New("dump failed")
		},
	)
	actionCalled := false

	err := collector.WithRequiredSnapshot(func() error {
		actionCalled = true
		return nil
	})
	if !errors.Is(err, ErrSnapshotFailed) {
		t.Fatalf("WithRequiredSnapshot() error = %v, want ErrSnapshotFailed", err)
	}
	if actionCalled {
		t.Fatal("snapshot action ran after an incomplete collection")
	}
	if _, ok := collector.GetStats("peer"); ok {
		t.Fatal("partial snapshot counters were committed")
	}
}

func TestCollectorBestEffortCollectsAvailableInterfaces(t *testing.T) {
	collector := NewCollector(
		t.TempDir(),
		func() []string { return []string{"awg0", "awg1"} },
		func(ifName string) ([]awg.PeerDump, error) {
			if ifName == "awg0" {
				return nil, errors.New("dump failed")
			}
			return []awg.PeerDump{{PublicKey: "peer", TransferRx: 10, TransferTx: 20}}, nil
		},
	)

	collector.Collect()
	stats, ok := collector.GetStats("peer")
	if !ok || stats.TotalRx != 10 || stats.TotalTx != 20 {
		t.Fatalf("best-effort stats = (%+v, %t), want rx=10 tx=20", stats, ok)
	}
}

func TestCollectorSaveLoadAndRemove(t *testing.T) {
	dataDir := t.TempDir()
	collector := NewCollector(
		dataDir,
		func() []string { return []string{"awg0"} },
		func(string) ([]awg.PeerDump, error) {
			return []awg.PeerDump{{PublicKey: "peer", TransferRx: 10, TransferTx: 20}}, nil
		},
	)
	collector.Collect()

	if err := collector.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(dataDir, "usage.json"))
	if err != nil {
		t.Fatalf("stat usage.json: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("usage.json permissions = %o, want 600", got)
	}

	loaded := NewCollector(dataDir, func() []string { return nil }, func(string) ([]awg.PeerDump, error) { return nil, nil })
	stats, ok := loaded.GetStats("peer")
	if !ok || stats.TotalRx != 10 || stats.TotalTx != 20 {
		t.Fatalf("loaded stats = (%+v, %t), want rx=10 tx=20", stats, ok)
	}

	loaded.RemoveStats("peer")
	if _, ok := loaded.GetStats("peer"); ok {
		t.Fatal("RemoveStats() left the peer in memory")
	}
}

func TestCollectorLoadRejectsInvalidData(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "null root", data: `null`},
		{name: "null peer", data: `{"peer":null}`},
		{name: "negative counter", data: `{"peer":{"total_rx":-1}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "usage.json")
			if err := os.WriteFile(path, []byte(tt.data), 0o600); err != nil {
				t.Fatalf("write usage data: %v", err)
			}

			collector := &Collector{filePath: path, stats: make(map[string]*PeerStats)}
			if err := collector.load(); err == nil {
				t.Fatal("load() accepted invalid usage data")
			}
		})
	}
}
