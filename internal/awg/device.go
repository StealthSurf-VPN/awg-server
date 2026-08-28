package awg

import (
	"errors"
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

func destroyInterface(ifName string) error {
	output, err := exec.Command("ip", "link", "del", ifName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip link del: %s: %w", string(output), err)
	}

	return nil
}

func configureDevice(ifName string, port int, profile Profile, privateKey [32]byte) error {
	cmd := exec.Command("awg", "setconf", ifName, "/dev/stdin")

	cmd.Stdin = strings.NewReader(profile.ServerConfig(privateKey, port))

	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("awg setconf: %w", err)
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

func addPeerToInterface(ifName string, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	if err := setPeerOnInterface(ifName, publicKey, presharedKey, allowedIP); err != nil {
		return err
	}

	return replacePeerRoute(ifName, allowedIP)
}

func setPeerOnInterface(ifName string, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	args := []string{"set", ifName, "peer", KeyToBase64(publicKey)}

	if presharedKey != nil {
		args = append(args, "preshared-key", "/dev/stdin")
	}

	args = append(args, "allowed-ips", allowedIP+"/32")

	cmd := exec.Command("awg", args...)

	if presharedKey != nil {
		cmd.Stdin = strings.NewReader(KeyToBase64(*presharedKey))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg set peer: %s: %w", string(output), err)
	}

	return nil
}

func replacePeerRoute(ifName, allowedIP string) error {
	if output, err := exec.Command(
		"ip", "route", "replace", allowedIP+"/32", "dev", ifName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("add peer route: %s: %w", string(output), err)
	}

	return nil
}

func removePeerFromInterface(ifName string, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	if err := removePeerOnlyFromInterface(ifName, publicKey); err != nil {
		return err
	}

	output, err := exec.Command("ip", "route", "del", allowedIP+"/32", "dev", ifName).CombinedOutput()
	if err == nil {
		return nil
	}

	removeRouteErr := fmt.Errorf("delete peer route: %s: %w", string(output), err)
	if restoreErr := restorePeerAndRoute(ifName, publicKey, presharedKey, allowedIP); restoreErr != nil {
		return errors.Join(removeRouteErr, fmt.Errorf("restore peer after route deletion failure: %w", restoreErr))
	}

	return removeRouteErr
}

func removePeerOnlyFromInterface(ifName string, publicKey [32]byte) error {
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

func cleanupPeerAfterFailedAdd(ifName string, publicKey [32]byte, allowedIP string) (bool, error) {
	var errs []error
	peerRemoved := true

	if err := removePeerOnlyFromInterface(ifName, publicKey); err != nil {
		peerRemoved = false
		errs = append(errs, err)
	}

	routeExists, err := peerRouteExists(ifName, allowedIP)
	if err != nil {
		errs = append(errs, err)
	} else if routeExists {
		output, deleteErr := exec.Command("ip", "route", "del", allowedIP+"/32", "dev", ifName).CombinedOutput()
		if deleteErr != nil {
			errs = append(errs, fmt.Errorf("delete partial peer route: %s: %w", string(output), deleteErr))
		}
	}

	return peerRemoved, errors.Join(errs...)
}

func peerRouteExists(ifName, allowedIP string) (bool, error) {
	output, err := exec.Command(
		"ip", "route", "show", "exact", allowedIP+"/32", "dev", ifName,
	).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("inspect partial peer route: %s: %w", string(output), err)
	}

	return strings.TrimSpace(string(output)) != "", nil
}

func restorePeerAndRoute(ifName string, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	var errs []error

	if err := setPeerOnInterface(ifName, publicKey, presharedKey, allowedIP); err != nil {
		errs = append(errs, fmt.Errorf("restore peer: %w", err))
	}

	if err := replacePeerRoute(ifName, allowedIP); err != nil {
		errs = append(errs, fmt.Errorf("restore peer route: %w", err))
	}

	return errors.Join(errs...)
}

type PeerDump struct {
	PublicKey     string
	TransferRx    int64
	TransferTx    int64
	LastHandshake time.Time
}

func ShowDump(ifName string) ([]PeerDump, error) {
	output, err := exec.Command("awg", "show", ifName, "dump").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("awg show dump: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return nil, nil
	}

	var peers []PeerDump

	// Peer line format: public_key \t preshared_key \t endpoint \t allowed_ips \t latest_handshake \t transfer_rx \t transfer_tx \t persistent_keepalive
	for index, line := range lines[1:] {
		row := index + 2
		fields := strings.Split(line, "\t")
		if len(fields) != 8 {
			return nil, fmt.Errorf("parse awg dump for interface %s: peer row %d has %d fields, want 8", ifName, row, len(fields))
		}

		if _, err := Base64ToKey(fields[0]); err != nil {
			return nil, fmt.Errorf("parse awg dump for interface %s: peer row %d has invalid public key", ifName, row)
		}

		rx, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil || rx < 0 {
			return nil, fmt.Errorf("parse awg dump for interface %s: peer row %d has invalid receive counter", ifName, row)
		}

		tx, err := strconv.ParseInt(fields[6], 10, 64)
		if err != nil || tx < 0 {
			return nil, fmt.Errorf("parse awg dump for interface %s: peer row %d has invalid transmit counter", ifName, row)
		}

		var handshake time.Time

		ts, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil || ts < 0 {
			return nil, fmt.Errorf("parse awg dump for interface %s: peer row %d has invalid handshake timestamp", ifName, row)
		}
		if ts > 0 {
			handshake = time.Unix(ts, 0)
		}

		peers = append(peers, PeerDump{
			PublicKey:     fields[0],
			TransferRx:    rx,
			TransferTx:    tx,
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
