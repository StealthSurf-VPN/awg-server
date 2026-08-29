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

type poolOperations interface {
	createInterface(string) error
	destroyInterface(string) error
	configureDevice(string, int, Profile, [32]byte) error
	configureInterfaceNetwork(string, string) error
	addPeerToInterface(string, [32]byte, *[32]byte, string) error
	removePeerFromInterface(string, [32]byte, *[32]byte, string) error
	removePeerOnlyFromInterface(string, [32]byte) error
	cleanupPeerAfterFailedAdd(string, [32]byte, string) (bool, error)
	restorePeerAndRoute(string, [32]byte, *[32]byte, string) error
	replacePeerRoute(string, string) error
	addMasquerade(string, string) error
	removeMasquerade(string, string) error
}

type hostPoolOperations struct{}

func (hostPoolOperations) createInterface(ifName string) error {
	return createInterface(ifName)
}

func (hostPoolOperations) destroyInterface(ifName string) error {
	return destroyInterface(ifName)
}

func (hostPoolOperations) configureDevice(ifName string, port int, profile Profile, privateKey [32]byte) error {
	return configureDevice(ifName, port, profile, privateKey)
}

func (hostPoolOperations) configureInterfaceNetwork(ifName, address string) error {
	return configureInterfaceNetwork(ifName, address)
}

func (hostPoolOperations) addPeerToInterface(ifName string, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	return addPeerToInterface(ifName, publicKey, presharedKey, allowedIP)
}

func (hostPoolOperations) removePeerFromInterface(ifName string, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	return removePeerFromInterface(ifName, publicKey, presharedKey, allowedIP)
}

func (hostPoolOperations) removePeerOnlyFromInterface(ifName string, publicKey [32]byte) error {
	return removePeerOnlyFromInterface(ifName, publicKey)
}

func (hostPoolOperations) cleanupPeerAfterFailedAdd(ifName string, publicKey [32]byte, allowedIP string) (bool, error) {
	return cleanupPeerAfterFailedAdd(ifName, publicKey, allowedIP)
}

func (hostPoolOperations) restorePeerAndRoute(ifName string, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	return restorePeerAndRoute(ifName, publicKey, presharedKey, allowedIP)
}

func (hostPoolOperations) replacePeerRoute(ifName, allowedIP string) error {
	return replacePeerRoute(ifName, allowedIP)
}

func (hostPoolOperations) addMasquerade(network, outIface string) error {
	output, err := exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
		"-s", network, "-o", outIface, "-j", "MASQUERADE").CombinedOutput()
	if err != nil {
		return fmt.Errorf("add masquerade rule: %s: %w", string(output), err)
	}

	return nil
}

func (hostPoolOperations) removeMasquerade(network, outIface string) error {
	if err := exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
		"-s", network, "-o", outIface, "-j", "MASQUERADE").Run(); err != nil {
		return fmt.Errorf("remove masquerade rule: %w", err)
	}

	return nil
}

type iface struct {
	ifName    string
	port      int
	profile   Profile
	peerCount int
	peerPSKs  map[[32]byte]*[32]byte
}

type Pool struct {
	mu         sync.Mutex
	cfg        *config.Config
	privKey    [32]byte
	pubKey     [32]byte
	outIface   string
	ifaces     map[ProfileKey]*iface
	orphans    map[string]int
	usedPorts  map[int]bool
	nextIndex  int
	maxIfaces  int
	masqAdded  bool
	operations poolOperations
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

	pool := &Pool{
		cfg:        cfg,
		privKey:    privateKey,
		pubKey:     PublicKeyFromPrivate(privateKey),
		outIface:   outIface,
		ifaces:     make(map[ProfileKey]*iface),
		orphans:    make(map[string]int),
		usedPorts:  make(map[int]bool),
		maxIfaces:  maxIfaces,
		operations: hostPoolOperations{},
	}

	if err := pool.ApplyLANIsolation(nil); err != nil {
		return nil, fmt.Errorf("initialize LAN firewall: %w", err)
	}

	return pool, nil
}

func (p *Pool) AddPeer(profile Profile, requestedPort int, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !profile.valid() {
		return errors.New("invalid profile")
	}

	key := profile.Key()

	ifc, err := p.getOrCreateInterface(profile, requestedPort)
	if err != nil {
		return fmt.Errorf("get or create interface: %w", err)
	}

	if err := p.addPeerLocked(key, ifc, publicKey, presharedKey, allowedIP); err != nil {
		return err
	}

	return nil
}

func (p *Pool) addPeerLocked(key ProfileKey, ifc *iface, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	if err := p.operations.addPeerToInterface(ifc.ifName, publicKey, presharedKey, allowedIP); err != nil {
		wasEmpty := ifc.peerCount == 0
		peerRemoved, cleanupErr := p.operations.cleanupPeerAfterFailedAdd(ifc.ifName, publicKey, allowedIP)

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

func (p *Pool) destroyTrackedInterfaceLocked(key ProfileKey, ifc *iface) error {
	if err := p.operations.destroyInterface(ifc.ifName); err != nil {
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

func (p *Pool) RemovePeer(profile Profile, publicKey [32]byte, allowedIP string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !profile.valid() {
		return errors.New("invalid profile")
	}

	key := profile.Key()

	ifc, ok := p.ifaces[key]
	if !ok {
		return errors.New("no interface for profile")
	}
	if ifc.peerCount <= 0 {
		return errors.New("invalid peer accounting for existing profile")
	}

	presharedKey, ok := ifc.peerPSKs[publicKey]
	if !ok {
		return errors.New("peer is not tracked on existing profile")
	}

	if err := p.operations.removePeerFromInterface(ifc.ifName, publicKey, presharedKey, allowedIP); err != nil {
		return fmt.Errorf("remove peer from interface: %w", err)
	}

	untrackPeerLocked(ifc, publicKey)

	if ifc.peerCount == 0 {
		log.Printf("destroying interface %s (no peers left)", ifc.ifName)
		if err := p.destroyTrackedInterfaceLocked(key, ifc); err != nil {
			destroyErr := fmt.Errorf("destroy empty interface: %w", err)
			restoreErr := p.operations.restorePeerAndRoute(ifc.ifName, publicKey, presharedKey, allowedIP)
			trackPeerLocked(ifc, publicKey, presharedKey)

			if restoreErr != nil {
				return rollbackFailure(destroyErr, fmt.Errorf("restore peer after interface destroy failure: %w", restoreErr))
			}

			return destroyErr
		}
	}

	return nil
}

func (p *Pool) MigratePeer(oldProfile, newProfile Profile, newRequestedPort int, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !oldProfile.valid() || !newProfile.valid() {
		return errors.New("invalid profile")
	}

	oldKey := oldProfile.Key()
	newKey := newProfile.Key()

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

	if oldKey == newKey {
		if oldProfile.Params().Port == newRequestedPort {
			return nil
		}
		if oldIfc.peerCount > 1 {
			return ErrPortShared
		}
	} else if newIfc, exists := p.ifaces[newKey]; exists {
		if err := validateExistingInterfacePort(newIfc, newRequestedPort); err != nil {
			return err
		}
	}

	if oldIfc.peerCount == 1 {
		oldPort := oldIfc.port

		if err := p.operations.removePeerFromInterface(oldIfc.ifName, publicKey, oldPresharedKey, allowedIP); err != nil {
			return fmt.Errorf("remove peer from old interface: %w", err)
		}

		untrackPeerLocked(oldIfc, publicKey)

		log.Printf("destroying interface %s (no peers left)", oldIfc.ifName)
		if err := p.destroyTrackedInterfaceLocked(oldKey, oldIfc); err != nil {
			destroyErr := fmt.Errorf("destroy old interface: %w", err)
			restoreErr := p.operations.restorePeerAndRoute(oldIfc.ifName, publicKey, oldPresharedKey, allowedIP)
			trackPeerLocked(oldIfc, publicKey, oldPresharedKey)

			if restoreErr != nil {
				return rollbackFailure(destroyErr, fmt.Errorf("restore old peer after interface destroy failure: %w", restoreErr))
			}

			return destroyErr
		}

		newIfc, err := p.getOrCreateInterface(newProfile, newRequestedPort)
		if err != nil {
			operationErr := fmt.Errorf("get or create interface: %w", err)
			if rollbackErr := p.rollbackPeer(oldProfile, oldPort, publicKey, oldPresharedKey, allowedIP); rollbackErr != nil {
				return rollbackFailure(operationErr, fmt.Errorf("restore old peer: %w", rollbackErr))
			}

			return operationErr
		}

		if err := p.addPeerLocked(newKey, newIfc, publicKey, presharedKey, allowedIP); err != nil {
			operationErr := fmt.Errorf("add peer to new interface: %w", err)
			if rollbackErr := p.rollbackPeer(oldProfile, oldPort, publicKey, oldPresharedKey, allowedIP); rollbackErr != nil {
				return rollbackFailure(operationErr, fmt.Errorf("restore old peer: %w", rollbackErr))
			}

			return operationErr
		}

		return nil
	}

	newIfc, err := p.getOrCreateInterface(newProfile, newRequestedPort)
	if err != nil {
		return fmt.Errorf("get or create interface: %w", err)
	}

	if err := p.addPeerLocked(newKey, newIfc, publicKey, presharedKey, allowedIP); err != nil {
		if routeErr := p.operations.replacePeerRoute(oldIfc.ifName, allowedIP); routeErr != nil {
			return rollbackFailure(err, fmt.Errorf("restore old route after failed peer add: %w", routeErr))
		}
		return err
	}

	if err := p.operations.removePeerOnlyFromInterface(oldIfc.ifName, publicKey); err != nil {
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
			restoreOldErr := p.operations.restorePeerAndRoute(oldIfc.ifName, publicKey, oldPresharedKey, allowedIP)
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

func (p *Pool) rollbackSharedMigration(oldIfc, newIfc *iface, newKey ProfileKey, publicKey [32]byte, oldPresharedKey *[32]byte, allowedIP string) error {
	var errs []error

	if err := p.rollbackNewPeerLocked(newIfc, newKey, publicKey, allowedIP); err != nil {
		errs = append(errs, fmt.Errorf("remove new peer: %w", err))
	}

	if err := p.operations.restorePeerAndRoute(oldIfc.ifName, publicKey, oldPresharedKey, allowedIP); err != nil {
		errs = append(errs, fmt.Errorf("restore old peer: %w", err))
	}

	return errors.Join(errs...)
}

func (p *Pool) rollbackNewPeerLocked(newIfc *iface, newKey ProfileKey, publicKey [32]byte, allowedIP string) error {
	presharedKey, ok := newIfc.peerPSKs[publicKey]
	if !ok {
		return errors.New("new peer is not tracked during rollback")
	}

	if err := p.operations.removePeerFromInterface(newIfc.ifName, publicKey, presharedKey, allowedIP); err != nil {
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

func (p *Pool) rollbackPeer(profile Profile, requestedPort int, publicKey [32]byte, presharedKey *[32]byte, allowedIP string) error {
	key := profile.Key()

	ifc, err := p.getOrCreateInterface(profile, requestedPort)
	if err != nil {
		return fmt.Errorf("recreate interface: %w", err)
	}

	if err := p.addPeerLocked(key, ifc, publicKey, presharedKey, allowedIP); err != nil {
		return fmt.Errorf("re-add peer: %w", err)
	}

	return nil
}

func (p *Pool) PortForProfile(profile Profile) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !profile.valid() {
		return 0, errors.New("invalid profile")
	}

	ifc, ok := p.ifaces[profile.Key()]
	if !ok {
		return 0, errors.New("no interface for profile")
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
		if err := p.operations.destroyInterface(ifName); err != nil {
			log.Printf("warning: failed to destroy orphan interface during pool close: interface=%s", ifName)
			continue
		}

		delete(p.orphans, ifName)
		delete(p.usedPorts, port)
	}

	if p.masqAdded && len(p.ifaces) == 0 && len(p.orphans) == 0 {
		if err := p.operations.removeMasquerade(p.cfg.Network().String(), p.outIface); err != nil {
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

func (p *Pool) getOrCreateInterface(profile Profile, requestedPort int) (*iface, error) {
	if !profile.valid() {
		return nil, errors.New("invalid profile")
	}

	key := profile.Key()

	if ifc, ok := p.ifaces[key]; ok {
		if err := validateExistingInterfacePort(ifc, requestedPort); err != nil {
			return nil, err
		}

		return ifc, nil
	}

	port := requestedPort
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

	if err := p.operations.createInterface(ifName); err != nil {
		return nil, fmt.Errorf("create interface %s: %w", ifName, err)
	}

	if err := p.operations.configureDevice(ifName, port, profile, p.privKey); err != nil {
		operationErr := fmt.Errorf("configure device %s: %w", ifName, err)
		if cleanupErr := p.operations.destroyInterface(ifName); cleanupErr != nil {
			p.trackOrphanInterfaceLocked(ifName, port)
			return nil, rollbackFailure(operationErr, fmt.Errorf("destroy incomplete interface: %w", cleanupErr))
		}

		return nil, operationErr
	}

	if err := p.operations.configureInterfaceNetwork(ifName, p.cfg.Address); err != nil {
		operationErr := fmt.Errorf("configure network %s: %w", ifName, err)
		if cleanupErr := p.operations.destroyInterface(ifName); cleanupErr != nil {
			p.trackOrphanInterfaceLocked(ifName, port)
			return nil, rollbackFailure(operationErr, fmt.Errorf("destroy incomplete interface: %w", cleanupErr))
		}

		return nil, operationErr
	}

	if !p.masqAdded {
		if err := p.operations.addMasquerade(p.cfg.Network().String(), p.outIface); err != nil {
			operationErr := fmt.Errorf("add masquerade rule: %w", err)
			if cleanupErr := p.operations.destroyInterface(ifName); cleanupErr != nil {
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
		profile:  profile,
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
