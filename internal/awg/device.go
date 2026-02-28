package awg

import (
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/stealthsurf-vpn/awg-server/internal/config"
)


type PeerInfo struct {
	PublicKey string
	AllowedIP string
}

type Device struct {
	cfg      *config.Config
	privKey  [32]byte
	pubKey   [32]byte
	ifName   string
	outIface string
}

func NewDevice(cfg *config.Config, privateKey [32]byte) (*Device, error) {
	publicKey := PublicKeyFromPrivate(privateKey)

	ifName := "awg0"

	if err := createInterface(ifName); err != nil {
		return nil, fmt.Errorf("create interface: %w", err)
	}

	if err := configureDevice(ifName, cfg, privateKey); err != nil {
		destroyInterface(ifName)
		return nil, fmt.Errorf("configure device: %w", err)
	}

	outIface, err := configureNetwork(ifName, cfg)
	if err != nil {
		destroyInterface(ifName)
		return nil, fmt.Errorf("configure network: %w", err)
	}

	log.Printf("AmneziaWG kernel device %s started on :%d, public key: %s", ifName, cfg.ListenPort, KeyToBase64(publicKey))

	return &Device{
		cfg:      cfg,
		privKey:  privateKey,
		pubKey:   publicKey,
		ifName:   ifName,
		outIface: outIface,
	}, nil
}

func (d *Device) AddPeer(publicKey [32]byte, allowedIP string) error {
	pubKeyB64 := KeyToBase64(publicKey)

	output, err := exec.Command(
		"awg", "set", d.ifName,
		"peer", pubKeyB64,
		"allowed-ips", allowedIP+"/32",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg set peer: %s: %w", string(output), err)
	}

	return nil
}

func (d *Device) RemovePeer(publicKey [32]byte) error {
	pubKeyB64 := KeyToBase64(publicKey)

	output, err := exec.Command(
		"awg", "set", d.ifName,
		"peer", pubKeyB64,
		"remove",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg remove peer: %s: %w", string(output), err)
	}

	return nil
}

func (d *Device) ListPeers() ([]PeerInfo, error) {
	output, err := exec.Command("awg", "show", d.ifName, "dump").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("awg show dump: %s: %w", string(output), err)
	}

	return parseDump(string(output)), nil
}

func (d *Device) PublicKey() [32]byte {
	return d.pubKey
}

func (d *Device) Close() {
	exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
		"-s", d.cfg.Network().String(), "-o", d.outIface, "-j", "MASQUERADE").Run()

	destroyInterface(d.ifName)
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

func configureDevice(ifName string, cfg *config.Config, privateKey [32]byte) error {
	args := []string{"set", ifName, "listen-port", fmt.Sprintf("%d", cfg.ListenPort)}

	args = append(args, "private-key", "/dev/stdin")

	if cfg.Jc > 0 {
		args = append(args, "jc", fmt.Sprintf("%d", cfg.Jc))
	}

	if cfg.Jmin > 0 {
		args = append(args, "jmin", fmt.Sprintf("%d", cfg.Jmin))
	}

	if cfg.Jmax > 0 {
		args = append(args, "jmax", fmt.Sprintf("%d", cfg.Jmax))
	}

	if cfg.S1 > 0 {
		args = append(args, "s1", fmt.Sprintf("%d", cfg.S1))
	}

	if cfg.S2 > 0 {
		args = append(args, "s2", fmt.Sprintf("%d", cfg.S2))
	}

	if cfg.S3 > 0 {
		args = append(args, "s3", fmt.Sprintf("%d", cfg.S3))
	}

	if cfg.S4 > 0 {
		args = append(args, "s4", fmt.Sprintf("%d", cfg.S4))
	}

	if cfg.H1 > 0 {
		args = append(args, "h1", fmt.Sprintf("%d", cfg.H1))
	}

	if cfg.H2 > 0 {
		args = append(args, "h2", fmt.Sprintf("%d", cfg.H2))
	}

	if cfg.H3 > 0 {
		args = append(args, "h3", fmt.Sprintf("%d", cfg.H3))
	}

	if cfg.H4 > 0 {
		args = append(args, "h4", fmt.Sprintf("%d", cfg.H4))
	}

	if cfg.I1 != "" {
		args = append(args, "i1", cfg.I1)
	}

	if cfg.I2 != "" {
		args = append(args, "i2", cfg.I2)
	}

	if cfg.I3 != "" {
		args = append(args, "i3", cfg.I3)
	}

	if cfg.I4 != "" {
		args = append(args, "i4", cfg.I4)
	}

	if cfg.I5 != "" {
		args = append(args, "i5", cfg.I5)
	}

	cmd := exec.Command("awg", args...)

	cmd.Stdin = strings.NewReader(KeyToBase64(privateKey))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg set: %s: %w", string(output), err)
	}

	return nil
}

func configureNetwork(ifName string, cfg *config.Config) (string, error) {
	outIface, err := detectDefaultInterface()
	if err != nil {
		return "", fmt.Errorf("detect default interface: %w", err)
	}

	if cfg.Interface != "" {
		outIface = cfg.Interface
	}

	log.Printf("using outbound interface: %s", outIface)

	commands := [][]string{
		{"ip", "addr", "add", cfg.Address, "dev", ifName},
		{"ip", "link", "set", ifName, "up"},
		{"iptables", "-t", "nat", "-A", "POSTROUTING", "-s", cfg.Network().String(), "-o", outIface, "-j", "MASQUERADE"},
	}

	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)

		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("command %v failed: %s: %w", args, string(output), err)
		}
	}

	return outIface, nil
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
		}

		if len(fields) > 3 {
			peer.AllowedIP = strings.TrimSuffix(fields[3], "/32")
		}

		peers = append(peers, peer)
	}

	return peers
}
