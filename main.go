package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stealthsurf-vpn/awg-server/internal/api"
	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/clients"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
	"github.com/stealthsurf-vpn/awg-server/internal/update"
	"github.com/stealthsurf-vpn/awg-server/internal/usage"
)

var version = "dev"

type startupState struct {
	cfg        *config.Config
	storage    *clients.Storage
	data       *clients.StorageData
	privateKey [32]byte
	defaults   clients.ManagerDefaults
}

type mainDependencies struct {
	checkRuntime       func() (awg.RuntimeDiagnostics, error)
	prepareStartup     func() (*startupState, error)
	prepareRestorePlan func(*startupState) (*clients.RestorePlan, error)
	startQualified     func(*startupState, *clients.RestorePlan) error
	runUpdate          func()
}

func main() {
	if err := runCommand(os.Args[1:], defaultMainDependencies(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func defaultMainDependencies() mainDependencies {
	return mainDependencies{
		checkRuntime:       awg.CheckRuntime,
		prepareStartup:     prepareStartup,
		prepareRestorePlan: prepareRestorePlan,
		startQualified:     startQualified,
		runUpdate:          runUpdate,
	}
}

func runCommand(args []string, dependencies mainDependencies, output io.Writer) error {
	if len(args) == 0 {
		return runApplication(dependencies)
	}

	switch args[0] {
	case "version":
		_, _ = fmt.Fprintf(output, "awg-server %s\n", version)
		return nil
	case "update":
		dependencies.runUpdate()
		return nil
	case "check-runtime":
		diagnostics, err := dependencies.checkRuntime()
		if err != nil {
			return fmt.Errorf("check AWG 3.1 runtime: %w", err)
		}

		writeRuntimeDiagnostics(output, diagnostics)
		return nil
	default:
		return fmt.Errorf("unknown command: %s\nusage: awg-server [version|update|check-runtime]", args[0])
	}
}

func writeRuntimeDiagnostics(output io.Writer, diagnostics awg.RuntimeDiagnostics) {
	_, _ = fmt.Fprintln(output, "AWG 3.1 runtime qualified")
	_, _ = fmt.Fprintf(output, "amneziawg-tools package: %s\n", diagnostics.ToolsPackageVersion)
	_, _ = fmt.Fprintf(output, "amneziawg-dkms package: %s\n", diagnostics.DKMSPackageVersion)
	_, _ = fmt.Fprintf(output, "tools version: %s\n", diagnostics.ToolsVersion)
	_, _ = fmt.Fprintf(output, "module version: %s\n", diagnostics.ModuleVersion)
}

func runApplication(dependencies mainDependencies) error {
	state, err := dependencies.prepareStartup()
	if err != nil {
		return err
	}
	plan, err := dependencies.prepareRestorePlan(state)
	if err != nil {
		return err
	}

	if _, err := dependencies.checkRuntime(); err != nil {
		return fmt.Errorf("check AWG 3.1 runtime: %w", err)
	}

	return dependencies.startQualified(state, plan)
}

func prepareStartup() (*startupState, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	storage := clients.NewStorage(cfg.DataDir)

	loadedData, err := storage.Load()
	if err != nil {
		return nil, fmt.Errorf("load storage: %w", err)
	}
	data, err := clients.PrepareStorageDefaults(loadedData)
	if err != nil {
		return nil, fmt.Errorf("prepare storage defaults: %w", err)
	}

	privateKey, err := awg.Base64ToKey(data.ServerPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decode server private key: %w", err)
	}
	defaults, err := temporaryManagerDefaults(cfg, data)
	if err != nil {
		return nil, err
	}

	return &startupState{
		cfg:        cfg,
		storage:    storage,
		data:       data,
		privateKey: privateKey,
		defaults:   defaults,
	}, nil
}

func prepareRestorePlan(state *startupState) (*clients.RestorePlan, error) {
	plan, err := clients.PrepareRestorePlan(state.cfg, state.defaults, state.data)
	if err != nil {
		return nil, fmt.Errorf("prepare restore plan: %w", err)
	}

	return plan, nil
}

func temporaryManagerDefaults(cfg *config.Config, data *clients.StorageData) (clients.ManagerDefaults, error) {
	if data.GeneratedParams == nil {
		return clients.ManagerDefaults{}, fmt.Errorf("prepare defaults: generated AWG params are required")
	}

	legacy := awg.AWGParams{
		MTU:  cfg.MTU,
		DNS:  cfg.DNS,
		Jc:   cfg.Jc,
		Jmin: cfg.Jmin,
		Jmax: cfg.Jmax,
		S1:   data.GeneratedParams.S1,
		S2:   data.GeneratedParams.S2,
		S3:   cfg.S3,
		S4:   cfg.S4,
		H1:   data.GeneratedParams.H1,
		H2:   data.GeneratedParams.H2,
		H3:   data.GeneratedParams.H3,
		H4:   data.GeneratedParams.H4,
		I1:   cfg.I1,
		I2:   cfg.I2,
		I3:   cfg.I3,
		I4:   cfg.I4,
		I5:   cfg.I5,
	}

	awg31, err := temporaryAWG31Defaults(cfg)
	if err != nil {
		return clients.ManagerDefaults{}, err
	}

	return clients.ManagerDefaults{
		LegacyParams:   legacy,
		AWG31Params:    awg31,
		DefaultVersion: awg.ProtocolVersion31,
	}, nil
}

func temporaryAWG31Defaults(cfg *config.Config) (awg.AWGParams, error) {
	persistentKeepalive, err := config.ParseUint16Range("25-35")
	if err != nil {
		return awg.AWGParams{}, err
	}
	contentPaddingAddition, err := config.ParseUint16Range("10-100")
	if err != nil {
		return awg.AWGParams{}, err
	}
	rekeyAfterTime, err := config.ParseUint16Range("100-120")
	if err != nil {
		return awg.AWGParams{}, err
	}
	rekeyTimeout, err := config.ParseUint16Range("3-7")
	if err != nil {
		return awg.AWGParams{}, err
	}
	rejectAfterTime, err := config.ParseUint16Range("150-180")
	if err != nil {
		return awg.AWGParams{}, err
	}
	keepaliveTimeout, err := config.ParseUint16Range("5-15")
	if err != nil {
		return awg.AWGParams{}, err
	}
	maxHandshakeAttempts, err := config.ParseUint16Range("15-20")
	if err != nil {
		return awg.AWGParams{}, err
	}

	return awg.AWGParams{
		MTU:                    1280,
		DNS:                    cfg.DNS,
		Jc:                     cfg.Jc,
		Jmin:                   cfg.Jmin,
		Jmax:                   cfg.Jmax,
		I1:                     cfg.I1,
		I2:                     cfg.I2,
		I3:                     cfg.I3,
		I4:                     cfg.I4,
		I5:                     cfg.I5,
		PersistentKeepalive:    &persistentKeepalive,
		ContentPaddingAddition: &contentPaddingAddition,
		RekeyAfterTime:         &rekeyAfterTime,
		RekeyTimeout:           &rekeyTimeout,
		RejectAfterTime:        &rejectAfterTime,
		KeepaliveTimeout:       &keepaliveTimeout,
		MaxHandshakeAttempts:   &maxHandshakeAttempts,
		RandomTrailers:         "on",
		DisableCookies:         "off",
	}, nil
}

func startQualified(state *startupState, plan *clients.RestorePlan) error {
	pool, err := awg.NewPool(state.cfg, state.privateKey, state.cfg.MaxInterfaces)
	if err != nil {
		return fmt.Errorf("create AWG pool: %w", err)
	}

	mgr, err := clients.NewManagerFromRestorePlan(pool, state.storage, state.cfg, plan)
	if err != nil {
		pool.Close()
		return fmt.Errorf("create client manager: %w", err)
	}

	collector := usage.NewCollector(state.cfg.DataDir, pool.InterfaceNames, awg.ShowDump)

	collectorCtx, collectorCancel := context.WithCancel(context.Background())
	collectorDone := make(chan struct{})

	go func() {
		defer close(collectorDone)
		collector.Run(collectorCtx)
	}()

	srv := api.NewServer(mgr, state.cfg, collector)

	serverErrCh := make(chan error, 1)

	go func() {
		serverErrCh <- srv.Start()
	}()

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var serverErr error

	select {
	case sig := <-sigCh:
		log.Printf("received signal %s, shutting down...", sig)
	case serverErr = <-serverErrCh:
		log.Printf("HTTP server stopped unexpectedly, shutting down: %v", serverErr)
	}

	collectorCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	<-collectorDone

	collector.Collect()

	if err := collector.Save(); err != nil {
		log.Printf("warning: failed to save final usage data: %v", err)
	}

	pool.Close()
	if serverErr != nil {
		return fmt.Errorf("HTTP server stopped unexpectedly: %w", serverErr)
	}

	log.Println("shutdown complete")

	return nil
}

func runUpdate() {
	u := update.New(version)

	result, err := u.Check()
	if err != nil {
		log.Fatalf("check for updates: %v", err)
	}

	if !result.NeedsUpdate {
		fmt.Printf("already up to date (%s)\n", result.Latest)
		return
	}

	fmt.Printf("updating %s -> %s...\n", version, result.Latest)

	if err := u.Apply(result); err != nil {
		log.Fatalf("apply update: %v", err)
	}

	fmt.Printf("updated to %s, restart the service to apply\n", result.Latest)
}
