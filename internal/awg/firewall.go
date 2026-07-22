package awg

import (
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strings"
)

const lanFirewallChain = "AWG-LAN"

type LANPeer struct {
	Address string
	GroupID string
}

func (p *Pool) ApplyLANIsolation(peers []LANPeer) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	network := p.cfg.Network().String()
	hookExists, err := lanFirewallHookExists(network)
	if err != nil {
		return err
	}

	rules, err := renderLANFirewall(network, peers, hookExists)
	if err != nil {
		return err
	}

	cmd := exec.Command("iptables-restore", "--wait", "5", "--noflush")
	cmd.Stdin = strings.NewReader(rules)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apply LAN firewall: %s: %w", string(output), err)
	}

	return nil
}

func lanFirewallHookExists(network string) (bool, error) {
	output, err := exec.Command("iptables", "--wait", "5", "-S", lanFirewallChain).CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}

		return false, fmt.Errorf("inspect LAN firewall chain: %s: %w", string(output), err)
	}

	args := []string{
		"--wait", "5", "-C", "FORWARD",
		"-i", "awg+", "-o", "awg+",
		"-s", network, "-d", network,
		"-j", lanFirewallChain,
	}

	output, err = exec.Command("iptables", args...).CombinedOutput()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}

	return false, fmt.Errorf("inspect LAN firewall hook: %s: %w", string(output), err)
}

func renderLANFirewall(network string, peers []LANPeer, hookExists bool) (string, error) {
	prefix, err := netip.ParsePrefix(network)
	if err != nil || !prefix.Addr().Is4() {
		return "", fmt.Errorf("parse VPN network %q", network)
	}

	prefix = prefix.Masked()
	normalized := append([]LANPeer(nil), peers...)
	addresses := make(map[netip.Addr]struct{}, len(normalized))

	for i := range normalized {
		address, err := netip.ParseAddr(normalized[i].Address)
		if err != nil || !address.Is4() {
			return "", fmt.Errorf("invalid LAN peer address %q", normalized[i].Address)
		}
		if !prefix.Contains(address) {
			return "", fmt.Errorf("LAN peer address %q is outside VPN network %s", address, prefix)
		}
		if normalized[i].GroupID == "" {
			return "", fmt.Errorf("LAN peer %q has empty LAN group", address)
		}
		if _, exists := addresses[address]; exists {
			return "", fmt.Errorf("duplicate LAN peer address %q", address)
		}

		addresses[address] = struct{}{}
		normalized[i].Address = address.String()
	}

	sort.Slice(normalized, func(i, j int) bool {
		left := netip.MustParseAddr(normalized[i].Address)
		right := netip.MustParseAddr(normalized[j].Address)

		return left.Compare(right) < 0
	})

	var rules strings.Builder

	fmt.Fprintln(&rules, "*filter")
	fmt.Fprintf(&rules, ":%s - [0:0]\n", lanFirewallChain)

	// ponytail: pairwise rules fit the IPv4 subnet ceiling; use ipset if larger groups are introduced.
	for _, source := range normalized {
		for _, destination := range normalized {
			if source.GroupID != destination.GroupID {
				continue
			}

			fmt.Fprintf(&rules, "-A %s -s %s/32 -d %s/32 -j ACCEPT\n",
				lanFirewallChain, source.Address, destination.Address)
		}
	}

	fmt.Fprintf(&rules, "-A %s -j DROP\n", lanFirewallChain)

	hook := fmt.Sprintf("-i awg+ -o awg+ -s %s -d %s -j %s", prefix, prefix, lanFirewallChain)
	if hookExists {
		fmt.Fprintf(&rules, "-D FORWARD %s\n", hook)
	}
	fmt.Fprintf(&rules, "-I FORWARD 1 %s\n", hook)
	fmt.Fprintln(&rules, "COMMIT")

	return rules.String(), nil
}
