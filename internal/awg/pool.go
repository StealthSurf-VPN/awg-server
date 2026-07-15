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
var ErrPortShared = errors.New("cannot change port on interface shared by multiple peers")
var ErrProfilePortConflict = errors.New("requested port does not match existing profile interface")
var ErrRollbackFailed = errors.New("rollback failed")

type rollbackError struct {
	operationErr error
	rollbackErr  error
}

func (e *rollbackError) Error() string {
	return fmt.Sprintf("%v; rollback failed: %v", e.operationErr, e.rollbackErr)
}

func (e *rollbackError) Unwrap() error {
	return ErrRollbackFailed
}

func rollbackFailure(operationErr, rollbackErr error) error {
	return &rollbackError{
		operationErr: operationErr,
		rollbackErr:  rollbackErr,
	}
}

type iface struct {
	ifName    string
	port      int
	params    AWGParams
	peerCount int
	peerPSKs  map[[32]byte]*[32]byte
}

type Pool struct {
	mu        sync.Mutex
	cfg       *config.Config
	privKey   [32]byte
	pubKey    [32]byte
	outIface  string
	ifaces    map[string]*iface
	orphans   map[string]int
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
		orphans:   make(map[string]int),
		usedPorts: make(map[int]bool),
		maxIfaces: maxIfaces,
	}, nil
}

func (p *Pool) AddPeer(params AWGParams, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := params.Key()

	ifc, err := p.getOrCreateInterface(params)
	if err != nil {
		return fmt.Errorf("get or create interface: %w", err)
	}

	if err := p.addPeerLocked(key, ifc, publicKey, presharedKey, allowedIP); err != nil {
		return err
	}

	return nil
}

func (p *Pool) addPeerLocked(key string, ifc *iface, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	if err := addPeerToInterface(ifc.ifName, publicKey, presharedKey, allowedIP); err != nil {
		wasEmpty := ifc.peerCount == 0
		peerRemoved, cleanupErr := cleanupPeerAfterFailedAdd(ifc.ifName, publicKey, allowedIP)

		if wasEmpty {
			destroyErr := p.destroyTrackedInterfaceLocked(key, ifc)
			if destroyErr == nil {
				return err
			}

			if !peerRemoved {
				trackPeerLocked(ifc, publicKey, presharedKey)
			}

			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("destroy empty interface: %w", destroyErr))
			log.Printf("warning: peer add cleanup failed: step=destroy_empty_interface interface=%s", ifc.ifName)
			return rollbackFailure(err, cleanupErr)
		}

		if !peerRemoved {
			trackPeerLocked(ifc, publicKey, presharedKey)
		}
		if cleanupErr != nil {
			log.Printf("warning: peer add cleanup failed: step=remove_partial_peer interface=%s", ifc.ifName)
			return rollbackFailure(err, cleanupErr)
		}

		return err
	}

	trackPeerLocked(ifc, publicKey, presharedKey)

	return nil
}

func trackPeerLocked(ifc *iface, publicKey [32]byte, presharedKey *[32]byte) {
	if ifc.peerPSKs == nil {
		ifc.peerPSKs = make(map[[32]byte]*[32]byte)
	}

	if _, exists := ifc.peerPSKs[publicKey]; !exists {
		ifc.peerCount++
	}

	if presharedKey == nil {
		ifc.peerPSKs[publicKey] = nil
		return
	}

	keyCopy := *presharedKey
	ifc.peerPSKs[publicKey] = &keyCopy
}

func untrackPeerLocked(ifc *iface, publicKey [32]byte) bool {
	if _, exists := ifc.peerPSKs[publicKey]; !exists {
		return false
	}

	delete(ifc.peerPSKs, publicKey)
	if ifc.peerCount > 0 {
		ifc.peerCount--
	}

	return true
}

func (p *Pool) destroyTrackedInterfaceLocked(key string, ifc *iface) error {
	if err := destroyInterface(ifc.ifName); err != nil {
		return err
	}

	delete(p.usedPorts, ifc.port)
	delete(p.ifaces, key)

	return nil
}

func (p *Pool) trackOrphanInterfaceLocked(ifName string, port int) {
	p.orphans[ifName] = port
	p.usedPorts[port] = true
}

func (p *Pool) RemovePeer(params AWGParams, publicKey [32]byte, allowedIP string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := params.Key()

	ifc, ok := p.ifaces[key]
	if !ok {
		return fmt.Errorf("no interface for params key %s", key)
	}
	if ifc.peerCount <= 0 {
		return errors.New("invalid peer accounting for existing profile")
	}

	presharedKey, ok := ifc.peerPSKs[publicKey]
	if !ok {
		return errors.New("peer is not tracked on existing profile")
	}

	if err := removePeerFromInterface(ifc.ifName, publicKey, presharedKey, allowedIP); err != nil {
		return fmt.Errorf("remove peer from interface: %w", err)
	}

	untrackPeerLocked(ifc, publicKey)

	if ifc.peerCount == 0 {
		log.Printf("destroying interface %s (no peers left)", ifc.ifName)
		if err := p.destroyTrackedInterfaceLocked(key, ifc); err != nil {
			destroyErr := fmt.Errorf("destroy empty interface: %w", err)
			restoreErr := restorePeerAndRoute(ifc.ifName, publicKey, presharedKey, allowedIP)
			trackPeerLocked(ifc, publicKey, presharedKey)

			if restoreErr != nil {
				return rollbackFailure(destroyErr, fmt.Errorf("restore peer after interface destroy failure: %w", restoreErr))
			}

			return destroyErr
		}
	}

	return nil
}

func (p *Pool) MigratePeer(oldParams, newParams AWGParams, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldKey := oldParams.Key()
	newKey := newParams.Key()

	oldIfc, ok := p.ifaces[oldKey]
	if !ok {
		return errors.New("no interface for existing profile")
	}
	if oldIfc.peerCount <= 0 {
		return errors.New("invalid peer accounting for existing profile")
	}

	oldPresharedKey, ok := oldIfc.peerPSKs[publicKey]
	if !ok {
		return errors.New("peer is not tracked on existing profile")
	}

	restoreParams := oldParams
	restoreParams.Port = oldIfc.port

	if oldKey == newKey {
		if oldParams.Port == newParams.Port {
			return nil
		}
		if oldIfc.peerCount > 1 {
			return ErrPortShared
		}
	} else if newIfc, exists := p.ifaces[newKey]; exists {
		if err := validateExistingInterfacePort(newIfc, newParams.Port); err != nil {
			return err
		}
	}

	// Last peer on old interface: remove first to free the port
	if oldIfc.peerCount == 1 {
		if err := removePeerFromInterface(oldIfc.ifName, publicKey, oldPresharedKey, allowedIP); err != nil {
			return fmt.Errorf("remove peer from old interface: %w", err)
		}

		untrackPeerLocked(oldIfc, publicKey)

		log.Printf("destroying interface %s (no peers left)", oldIfc.ifName)
		if err := p.destroyTrackedInterfaceLocked(oldKey, oldIfc); err != nil {
			destroyErr := fmt.Errorf("destroy old interface: %w", err)
			restoreErr := restorePeerAndRoute(oldIfc.ifName, publicKey, oldPresharedKey, allowedIP)
			trackPeerLocked(oldIfc, publicKey, oldPresharedKey)

			if restoreErr != nil {
				return rollbackFailure(destroyErr, fmt.Errorf("restore old peer after interface destroy failure: %w", restoreErr))
			}

			return destroyErr
		}

		newIfc, err := p.getOrCreateInterface(newParams)
		if err != nil {
			operationErr := fmt.Errorf("get or create interface: %w", err)
			if rollbackErr := p.rollbackPeer(restoreParams, publicKey, oldPresharedKey, allowedIP); rollbackErr != nil {
				return rollbackFailure(operationErr, fmt.Errorf("restore old peer: %w", rollbackErr))
			}

			return operationErr
		}

		if err := p.addPeerLocked(newKey, newIfc, publicKey, presharedKey, allowedIP); err != nil {
			operationErr := fmt.Errorf("add peer to new interface: %w", err)
			if rollbackErr := p.rollbackPeer(restoreParams, publicKey, oldPresharedKey, allowedIP); rollbackErr != nil {
				return rollbackFailure(operationErr, fmt.Errorf("restore old peer: %w", rollbackErr))
			}

			return operationErr
		}

		return nil
	}

	// Multiple peers on old interface: add to new first, then remove from old
	newIfc, err := p.getOrCreateInterface(newParams)
	if err != nil {
		return fmt.Errorf("get or create interface: %w", err)
	}

	if err := p.addPeerLocked(newKey, newIfc, publicKey, presharedKey, allowedIP); err != nil {
		if routeErr := replacePeerRoute(oldIfc.ifName, allowedIP); routeErr != nil {
			return rollbackFailure(err, fmt.Errorf("restore old route after failed peer add: %w", routeErr))
		}
		return err
	}

	if err := removePeerOnlyFromInterface(oldIfc.ifName, publicKey); err != nil {
		operationErr := fmt.Errorf("remove peer from old interface: %w", err)
		if rollbackErr := p.rollbackSharedMigration(oldIfc, newIfc, newKey, publicKey, oldPresharedKey, allowedIP); rollbackErr != nil {
			return rollbackFailure(operationErr, fmt.Errorf("rollback shared peer migration: %w", rollbackErr))
		}

		return operationErr
	}

	untrackPeerLocked(oldIfc, publicKey)

	if oldIfc.peerCount == 0 {
		log.Printf("destroying interface %s (no peers left)", oldIfc.ifName)
		if err := p.destroyTrackedInterfaceLocked(oldKey, oldIfc); err != nil {
			destroyErr := fmt.Errorf("destroy old interface: %w", err)
			rollbackNewErr := p.rollbackNewPeerLocked(newIfc, newKey, publicKey, allowedIP)
			restoreOldErr := restorePeerAndRoute(oldIfc.ifName, publicKey, oldPresharedKey, allowedIP)
			trackPeerLocked(oldIfc, publicKey, oldPresharedKey)

			var rollbackErrs []error
			if rollbackNewErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("remove new peer: %w", rollbackNewErr))
			}
			if restoreOldErr != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("restore old peer: %w", restoreOldErr))
			}
			if rollbackErr := errors.Join(rollbackErrs...); rollbackErr != nil {
				return rollbackFailure(destroyErr, fmt.Errorf("rollback shared peer migration: %w", rollbackErr))
			}

			return destroyErr
		}
	}

	return nil
}

func (p *Pool) rollbackSharedMigration(oldIfc, newIfc *iface, newKey string, publicKey [32]byte, oldPresharedKey *[32]byte, allowedIP string) error {
	var errs []error

	if err := p.rollbackNewPeerLocked(newIfc, newKey, publicKey, allowedIP); err != nil {
		errs = append(errs, fmt.Errorf("remove new peer: %w", err))
	}

	if err := restorePeerAndRoute(oldIfc.ifName, publicKey, oldPresharedKey, allowedIP); err != nil {
		errs = append(errs, fmt.Errorf("restore old peer: %w", err))
	}

	return errors.Join(errs...)
}

func (p *Pool) rollbackNewPeerLocked(newIfc *iface, newKey string, publicKey [32]byte, allowedIP string) error {
	presharedKey, ok := newIfc.peerPSKs[publicKey]
	if !ok {
		return errors.New("new peer is not tracked during rollback")
	}

	if err := removePeerFromInterface(newIfc.ifName, publicKey, presharedKey, allowedIP); err != nil {
		return err
	}

	untrackPeerLocked(newIfc, publicKey)
	if newIfc.peerCount == 0 {
		log.Printf("destroying interface %s (shared migration rolled back)", newIfc.ifName)
		if err := p.destroyTrackedInterfaceLocked(newKey, newIfc); err != nil {
			return fmt.Errorf("destroy new interface: %w", err)
		}
	}

	return nil
}

func (p *Pool) rollbackPeer(params AWGParams, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	key := params.Key()

	ifc, err := p.getOrCreateInterface(params)
	if err != nil {
		return fmt.Errorf("recreate interface: %w", err)
	}

	if err := p.addPeerLocked(key, ifc, publicKey, presharedKey, allowedIP); err != nil {
		return fmt.Errorf("re-add peer: %w", err)
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

	for key, ifc := range p.ifaces {
		if err := p.destroyTrackedInterfaceLocked(key, ifc); err != nil {
			log.Printf("warning: failed to destroy interface during pool close: interface=%s", ifc.ifName)
		}
	}

	for ifName, port := range p.orphans {
		if err := destroyInterface(ifName); err != nil {
			log.Printf("warning: failed to destroy orphan interface during pool close: interface=%s", ifName)
			continue
		}

		delete(p.orphans, ifName)
		delete(p.usedPorts, port)
	}

	if p.masqAdded && len(p.ifaces) == 0 && len(p.orphans) == 0 {
		if err := exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
			"-s", p.cfg.Network().String(), "-o", p.outIface, "-j", "MASQUERADE").Run(); err != nil {
			log.Printf("warning: failed to remove MASQUERADE rule: %v", err)
		} else {
			p.masqAdded = false
		}
	}
}

func (p *Pool) InterfaceNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	names := make([]string, 0, len(p.ifaces))

	for _, ifc := range p.ifaces {
		names = append(names, ifc.ifName)
	}

	return names
}

func (p *Pool) getOrCreateInterface(params AWGParams) (*iface, error) {
	key := params.Key()

	if ifc, ok := p.ifaces[key]; ok {
		if err := validateExistingInterfacePort(ifc, params.Port); err != nil {
			return nil, err
		}

		return ifc, nil
	}

	port := params.Port
	if port != 0 {
		resolvedPort, err := p.resolvePort(port)
		if err != nil {
			return nil, err
		}

		port = resolvedPort
	}

	if p.maxIfaces > 0 && len(p.ifaces)+len(p.orphans) >= p.maxIfaces {
		return nil, ErrMaxInterfacesReached
	}

	if port == 0 {
		resolvedPort, err := p.resolvePort(0)
		if err != nil {
			return nil, err
		}

		port = resolvedPort
	}

	ifName := fmt.Sprintf("awg%d", p.nextIndex)

	p.nextIndex++

	if err := createInterface(ifName); err != nil {
		return nil, fmt.Errorf("create interface %s: %w", ifName, err)
	}

	if err := configureDevice(ifName, port, params, p.privKey); err != nil {
		operationErr := fmt.Errorf("configure device %s: %w", ifName, err)
		if cleanupErr := destroyInterface(ifName); cleanupErr != nil {
			p.trackOrphanInterfaceLocked(ifName, port)
			return nil, rollbackFailure(operationErr, fmt.Errorf("destroy incomplete interface: %w", cleanupErr))
		}

		return nil, operationErr
	}

	if err := configureInterfaceNetwork(ifName, p.cfg.Address); err != nil {
		operationErr := fmt.Errorf("configure network %s: %w", ifName, err)
		if cleanupErr := destroyInterface(ifName); cleanupErr != nil {
			p.trackOrphanInterfaceLocked(ifName, port)
			return nil, rollbackFailure(operationErr, fmt.Errorf("destroy incomplete interface: %w", cleanupErr))
		}

		return nil, operationErr
	}

	if !p.masqAdded {
		output, err := exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
			"-s", p.cfg.Network().String(), "-o", p.outIface, "-j", "MASQUERADE").CombinedOutput()
		if err != nil {
			operationErr := fmt.Errorf("add masquerade rule: %s: %w", string(output), err)
			if cleanupErr := destroyInterface(ifName); cleanupErr != nil {
				p.trackOrphanInterfaceLocked(ifName, port)
				return nil, rollbackFailure(operationErr, fmt.Errorf("destroy incomplete interface: %w", cleanupErr))
			}

			return nil, operationErr
		}

		p.masqAdded = true
	}

	log.Printf("created AmneziaWG interface %s on :%d, public key: %s",
		ifName, port, KeyToBase64(p.pubKey))

	ifc := &iface{
		ifName:   ifName,
		port:     port,
		params:   params,
		peerPSKs: make(map[[32]byte]*[32]byte),
	}

	p.ifaces[key] = ifc
	p.usedPorts[port] = true

	return ifc, nil
}

func validateExistingInterfacePort(ifc *iface, requestedPort int) error {
	if requestedPort != 0 && requestedPort != ifc.port {
		return ErrProfilePortConflict
	}

	return nil
}

func (p *Pool) resolvePort(requested int) (int, error) {
	if err := ValidatePort(requested); err != nil {
		return 0, err
	}

	if requested > 0 {
		if p.usedPorts[requested] {
			return 0, fmt.Errorf("port %d: %w", requested, ErrPortInUse)
		}

		return requested, nil
	}

	port := p.cfg.ListenPort

	for p.usedPorts[port] && port <= MaxPort {
		port++
	}

	if port > MaxPort {
		return 0, fmt.Errorf("no available ports (exhausted range)")
	}

	return port, nil
}
