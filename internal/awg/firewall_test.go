package awg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLANFirewallHookExistsWhenChainIsMissing(t *testing.T) {
	binDir := t.TempDir()
	iptablesPath := filepath.Join(binDir, "iptables")
	script := `#!/bin/sh
case "$*" in
  *"-S AWG-LAN"*) exit 1 ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(iptablesPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake iptables: %v", err)
	}
	t.Setenv("PATH", binDir)

	exists, err := lanFirewallHookExists("10.100.0.0/24")
	if err != nil {
		t.Fatalf("lanFirewallHookExists() error = %v", err)
	}
	if exists {
		t.Fatal("lanFirewallHookExists() = true, want false")
	}
}

func TestRenderLANFirewall(t *testing.T) {
	peers := []LANPeer{
		{Address: "10.100.0.4", GroupID: "peer:other"},
		{Address: "10.100.0.3", GroupID: "peer:primary"},
		{Address: "10.100.0.2", GroupID: "peer:primary"},
	}

	got, err := renderLANFirewall("10.100.0.1/24", peers, false)
	if err != nil {
		t.Fatalf("renderLANFirewall() error = %v", err)
	}

	want := `*filter
:AWG-LAN - [0:0]
-A AWG-LAN -s 10.100.0.2/32 -d 10.100.0.2/32 -j ACCEPT
-A AWG-LAN -s 10.100.0.2/32 -d 10.100.0.3/32 -j ACCEPT
-A AWG-LAN -s 10.100.0.3/32 -d 10.100.0.2/32 -j ACCEPT
-A AWG-LAN -s 10.100.0.3/32 -d 10.100.0.3/32 -j ACCEPT
-A AWG-LAN -s 10.100.0.4/32 -d 10.100.0.4/32 -j ACCEPT
-A AWG-LAN -j DROP
-I FORWARD 1 -i awg+ -o awg+ -s 10.100.0.0/24 -d 10.100.0.0/24 -j AWG-LAN
COMMIT
`

	if got != want {
		t.Fatalf("renderLANFirewall() =\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderLANFirewallEmptyChainDropsAndRepositionsHook(t *testing.T) {
	got, err := renderLANFirewall("10.100.0.0/24", nil, true)
	if err != nil {
		t.Fatalf("renderLANFirewall() error = %v", err)
	}

	want := `*filter
:AWG-LAN - [0:0]
-A AWG-LAN -j DROP
-D FORWARD -i awg+ -o awg+ -s 10.100.0.0/24 -d 10.100.0.0/24 -j AWG-LAN
-I FORWARD 1 -i awg+ -o awg+ -s 10.100.0.0/24 -d 10.100.0.0/24 -j AWG-LAN
COMMIT
`

	if got != want {
		t.Fatalf("renderLANFirewall() =\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderLANFirewallRejectsInvalidPeers(t *testing.T) {
	tests := []struct {
		name    string
		network string
		peers   []LANPeer
		wantErr string
	}{
		{
			name:    "invalid network",
			network: "invalid",
			wantErr: "parse VPN network",
		},
		{
			name:    "invalid address",
			network: "10.100.0.0/24",
			peers:   []LANPeer{{Address: "invalid", GroupID: "peer:one"}},
			wantErr: "invalid LAN peer address",
		},
		{
			name:    "address outside network",
			network: "10.100.0.0/24",
			peers:   []LANPeer{{Address: "10.101.0.2", GroupID: "peer:one"}},
			wantErr: "outside VPN network",
		},
		{
			name:    "empty group",
			network: "10.100.0.0/24",
			peers:   []LANPeer{{Address: "10.100.0.2"}},
			wantErr: "empty LAN group",
		},
		{
			name:    "duplicate address",
			network: "10.100.0.0/24",
			peers: []LANPeer{
				{Address: "10.100.0.2", GroupID: "peer:one"},
				{Address: "10.100.0.2", GroupID: "peer:two"},
			},
			wantErr: "duplicate LAN peer address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := renderLANFirewall(tt.network, tt.peers, false)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("renderLANFirewall() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
