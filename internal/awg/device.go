package awg

import (
	"fmt"
	"os/exec"
	"strings"
)

type PeerInfo struct {
	PublicKey string
	AllowedIP string
}

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

	return nil
}

func removePeerFromInterface(ifName string, publicKey [32]byte) error {
	pubKeyB64 := KeyToBase64(publicKey)

	output, err := exec.Command(
		"awg", "set", ifName,
		"peer", pubKeyB64,
		"remove",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg remove peer: %s: %w", string(output), err)
	}

	return nil
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

func parseDump(output string) []PeerInfo {
	var peers []PeerInfo

	lines := strings.Split(strings.TrimSpace(output), "\n")

	for i, line := range lines {
		if i == 0 {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}

		peer := PeerInfo{
			PublicKey: fields[0],
			AllowedIP: strings.TrimSuffix(fields[3], "/32"),
		}

		peers = append(peers, peer)
	}

	return peers
}
