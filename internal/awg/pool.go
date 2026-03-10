package awg

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"sync"

	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

var ErrMaxInterfacesReached = errors.New("maximum number of interfaces reached")
var ErrPortInUse = errors.New("port already in use by another interface")

type iface struct {
	ifName    string
	port      int
	params    AWGParams
	peerCount int
}

type Pool struct {
	mu        sync.Mutex
	cfg       *config.Config
	privKey   [32]byte
	pubKey    [32]byte
	outIface  string
	ifaces    map[string]*iface
	usedPorts map[int]bool
	nextIndex int
	maxIfaces int
	masqAdded bool
}

func NewPool(cfg *config.Config, privateKey [32]byte, maxIfaces int) (*Pool, error) {
	outIface, err := detectDefaultInterface()
	if err != nil {
		return nil, fmt.Errorf("detect default interface: %w", err)
	}

	if cfg.Interface != "" {
		outIface = cfg.Interface
	}

	log.Printf("using outbound interface: %s", outIface)

	return &Pool{
		cfg:       cfg,
		privKey:   privateKey,
		pubKey:    PublicKeyFromPrivate(privateKey),
		outIface:  outIface,
		ifaces:    make(map[string]*iface),
		usedPorts: make(map[int]bool),
		maxIfaces: maxIfaces,
	}, nil
}

func (p *Pool) AddPeer(params AWGParams, publicKey [32]byte, allowedIP string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ifc, err := p.getOrCreateInterface(params)
	if err != nil {
		return fmt.Errorf("get or create interface: %w", err)
	}

	if err := addPeerToInterface(ifc.ifName, publicKey, allowedIP); err != nil {
		return err
	}

	ifc.peerCount++

	return nil
}

func (p *Pool) RemovePeer(params AWGParams, publicKey [32]byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := params.Key()

	ifc, ok := p.ifaces[key]
	if !ok {
		return fmt.Errorf("no interface for params key %s", key)
	}

	if err := removePeerFromInterface(ifc.ifName, publicKey); err != nil {
		return err
	}

	ifc.peerCount--

	if ifc.peerCount <= 0 {
		log.Printf("destroying interface %s (no peers left)", ifc.ifName)
		destroyInterface(ifc.ifName)
		delete(p.usedPorts, ifc.port)
		delete(p.ifaces, key)
	}

	return nil
}

func (p *Pool) PortForParams(params AWGParams) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := params.Key()

	ifc, ok := p.ifaces[key]
	if !ok {
		return 0, fmt.Errorf("no interface for params key %s", key)
	}

	return ifc.port, nil
}

func (p *Pool) PublicKey() [32]byte {
	return p.pubKey
}

func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, ifc := range p.ifaces {
		destroyInterface(ifc.ifName)
	}

	p.ifaces = make(map[string]*iface)
	p.usedPorts = make(map[int]bool)

	if p.masqAdded {
		exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
			"-s", p.cfg.Network().String(), "-o", p.outIface, "-j", "MASQUERADE").Run()
	}
}

func (p *Pool) getOrCreateInterface(params AWGParams) (*iface, error) {
	key := params.Key()

	if ifc, ok := p.ifaces[key]; ok {
		return ifc, nil
	}

	if p.maxIfaces > 0 && len(p.ifaces) >= p.maxIfaces {
		return nil, ErrMaxInterfacesReached
	}

	port, err := p.resolvePort(params.Port)
	if err != nil {
		return nil, err
	}

	ifName := fmt.Sprintf("awg%d", p.nextIndex)

	p.nextIndex++

	if err := createInterface(ifName); err != nil {
		return nil, fmt.Errorf("create interface %s: %w", ifName, err)
	}

	if err := configureDevice(ifName, port, params, p.privKey); err != nil {
		destroyInterface(ifName)
		return nil, fmt.Errorf("configure device %s: %w", ifName, err)
	}

	if err := configureInterfaceNetwork(ifName, p.cfg.Address); err != nil {
		destroyInterface(ifName)
		return nil, fmt.Errorf("configure network %s: %w", ifName, err)
	}

	if !p.masqAdded {
		output, err := exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
			"-s", p.cfg.Network().String(), "-o", p.outIface, "-j", "MASQUERADE").CombinedOutput()
		if err != nil {
			destroyInterface(ifName)
			return nil, fmt.Errorf("add masquerade rule: %s: %w", string(output), err)
		}

		p.masqAdded = true
	}

	log.Printf("created AmneziaWG interface %s on :%d, public key: %s",
		ifName, port, KeyToBase64(p.pubKey))

	ifc := &iface{
		ifName: ifName,
		port:   port,
		params: params,
	}

	p.ifaces[key] = ifc
	p.usedPorts[port] = true

	return ifc, nil
}

func (p *Pool) resolvePort(requested int) (int, error) {
	if requested > 0 {
		if p.usedPorts[requested] {
			return 0, fmt.Errorf("port %d: %w", requested, ErrPortInUse)
		}

		return requested, nil
	}

	port := p.cfg.ListenPort + p.nextIndex

	for p.usedPorts[port] {
		port++
	}

	return port, nil
}
