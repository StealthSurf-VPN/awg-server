package awg

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func createInterface(ifName string) error {
	output, err := exec.Command("ip", "link", "add", ifName, "type", "amneziawg").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(output), err)
	}

	return nil
}

func destroyInterface(ifName string) {
	exec.Command("ip", "link", "del", ifName).Run()
}

func configureDevice(ifName string, port int, params AWGParams, privateKey [32]byte) error {
	args := []string{"set", ifName, "listen-port", fmt.Sprintf("%d", port), "private-key", "/dev/stdin"}

	args = append(args, params.CLIArgs()...)

	cmd := exec.Command("awg", args...)

	cmd.Stdin = strings.NewReader(KeyToBase64(privateKey))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg set: %s: %w", string(output), err)
	}

	return nil
}

func configureInterfaceNetwork(ifName string, address string) error {
	commands := [][]string{
		{"ip", "addr", "add", address, "dev", ifName},
		{"ip", "link", "set", ifName, "up"},
	}

	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)

		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("command %v failed: %s: %w", args, string(output), err)
		}
	}

	return nil
}

func addPeerToInterface(ifName string, publicKey [32]byte, allowedIP string) error {
	pubKeyB64 := KeyToBase64(publicKey)

	output, err := exec.Command(
		"awg", "set", ifName,
		"peer", pubKeyB64,
		"allowed-ips", allowedIP+"/32",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg set peer: %s: %w", string(output), err)
	}

	if output, err := exec.Command(
		"ip", "route", "replace", allowedIP+"/32", "dev", ifName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("add peer route: %s: %w", string(output), err)
	}

	return nil
}

func removePeerFromInterface(ifName string, publicKey [32]byte, allowedIP string) error {
	pubKeyB64 := KeyToBase64(publicKey)

	output, err := exec.Command(
		"awg", "set", ifName,
		"peer", pubKeyB64,
		"remove",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg remove peer: %s: %w", string(output), err)
	}

	exec.Command("ip", "route", "del", allowedIP+"/32", "dev", ifName).Run()

	return nil
}

type PeerDump struct {
	PublicKey     string
	TransferRx   int64
	TransferTx   int64
	LastHandshake time.Time
}

func ShowDump(ifName string) ([]PeerDump, error) {
	output, err := exec.Command("awg", "show", ifName, "dump").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("awg show dump: %s: %w", string(output), err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return nil, nil
	}

	var peers []PeerDump

	// Peer line format: public_key \t preshared_key \t endpoint \t allowed_ips \t latest_handshake \t transfer_rx \t transfer_tx \t persistent_keepalive
	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			continue
		}

		rx, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil {
			continue
		}

		tx, err := strconv.ParseInt(fields[6], 10, 64)
		if err != nil {
			continue
		}

		var handshake time.Time

		if ts, err := strconv.ParseInt(fields[4], 10, 64); err == nil && ts > 0 {
			handshake = time.Unix(ts, 0)
		}

		peers = append(peers, PeerDump{
			PublicKey:     fields[0],
			TransferRx:   rx,
			TransferTx:   tx,
			LastHandshake: handshake,
		})
	}

	return peers, nil
}

func detectDefaultInterface() (string, error) {
	output, err := exec.Command("ip", "route", "show", "default").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ip route show default: %s: %w", string(output), err)
	}

	fields := strings.Fields(strings.TrimSpace(string(output)))

	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}

	return "", fmt.Errorf("no default route found")
}

