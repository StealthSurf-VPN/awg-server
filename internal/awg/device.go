package awg

import (
	"fmt"
	"os/exec"
	"strings"
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

