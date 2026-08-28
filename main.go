package main

import (
	"context"
	"errors"
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
	cfg           *config.Config
	storage       *clients.Storage
	data          *clients.StorageData
	privateKey    [32]byte
	defaultParams awg.AWGParams
}

type mainDependencies struct {
	checkRuntime   func() (awg.RuntimeDiagnostics, error)
	prepareStartup func() (*startupState, error)
	startQualified func(*startupState) error
	runUpdate      func()
}

func main() {
	if err := runCommand(os.Args[1:], defaultMainDependencies(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func defaultMainDependencies() mainDependencies {
	return mainDependencies{
		checkRuntime:   awg.CheckRuntime,
		prepareStartup: prepareStartup,
		startQualified: startQualified,
		runUpdate:      runUpdate,
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

	if _, err := dependencies.checkRuntime(); err != nil {
		return fmt.Errorf("check AWG 3.1 runtime: %w", err)
	}

	return dependencies.startQualified(state)
}

func prepareStartup() (*startupState, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	storage := clients.NewStorage(cfg.DataDir)

	data, err := storage.Load()
	if err != nil {
		return nil, fmt.Errorf("load storage: %w", err)
	}
	if len(data.Clients) > 0 && (data.ServerPrivateKey == "" || data.GeneratedParams == nil) {
		return nil, errors.New("validate storage: persisted clients require server_private_key and generated_params")
	}

	var privateKey [32]byte

	if data.ServerPrivateKey != "" {
		privateKey, err = awg.Base64ToKey(data.ServerPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("decode server private key: %w", err)
		}

		log.Println("loaded server private key from storage")
	} else {
		privateKey, err = awg.GeneratePrivateKey()
		if err != nil {
			return nil, fmt.Errorf("generate server private key: %w", err)
		}

		data.ServerPrivateKey = awg.KeyToBase64(privateKey)

		if err := storage.Save(data); err != nil {
			return nil, fmt.Errorf("save server private key: %w", err)
		}

		log.Println("generated new server private key")
	}

	if data.GeneratedParams != nil {
		log.Println("loaded generated AWG params from storage")
	} else {
		gp, err := awg.GenerateParams()
		if err != nil {
			return nil, fmt.Errorf("generate AWG params: %w", err)
		}

		data.GeneratedParams = gp

		if err := storage.Save(data); err != nil {
			return nil, fmt.Errorf("save generated AWG params: %w", err)
		}

		log.Printf("generated new AWG params: H1=%s H2=%s H3=%s H4=%s S1=%d S2=%d",
			gp.H1, gp.H2, gp.H3, gp.H4, gp.S1, gp.S2)
	}

	gp := data.GeneratedParams

	defaultParams := awg.AWGParams{
		MTU:  cfg.MTU,
		DNS:  cfg.DNS,
		Jc:   cfg.Jc,
		Jmin: cfg.Jmin,
		Jmax: cfg.Jmax,
		S1:   gp.S1,
		S2:   gp.S2,
		S3:   cfg.S3,
		S4:   cfg.S4,
		H1:   gp.H1,
		H2:   gp.H2,
		H3:   gp.H3,
		H4:   gp.H4,
		I1:   cfg.I1,
		I2:   cfg.I2,
		I3:   cfg.I3,
		I4:   cfg.I4,
		I5:   cfg.I5,
	}

	if err := awg.ValidateProfile(defaultParams); err != nil {
		return nil, fmt.Errorf("validate default AWG params: %w", err)
	}

	return &startupState{
		cfg:           cfg,
		storage:       storage,
		data:          data,
		privateKey:    privateKey,
		defaultParams: defaultParams,
	}, nil
}

func startQualified(state *startupState) error {
	pool, err := awg.NewPool(state.cfg, state.privateKey, state.cfg.MaxInterfaces)
	if err != nil {
		return fmt.Errorf("create AWG pool: %w", err)
	}

	mgr, err := clients.NewManager(pool, state.storage, state.cfg, state.defaultParams, state.data)
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
