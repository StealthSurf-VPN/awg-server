package main

import (
	"context"
	"fmt"
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

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Printf("awg-server %s\n", version)
			return
		case "update":
			runUpdate()
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\nusage: awg-server [version|update]\n", os.Args[1])
			os.Exit(1)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	storage := clients.NewStorage(cfg.DataDir)

	data, err := storage.Load()
	if err != nil {
		log.Fatalf("load storage: %v", err)
	}

	var privateKey [32]byte

	if data.ServerPrivateKey != "" {
		privateKey, err = awg.Base64ToKey(data.ServerPrivateKey)
		if err != nil {
			log.Fatalf("decode server private key: %v", err)
		}

		log.Println("loaded server private key from storage")
	} else {
		privateKey, err = awg.GeneratePrivateKey()
		if err != nil {
			log.Fatalf("generate server private key: %v", err)
		}

		data.ServerPrivateKey = awg.KeyToBase64(privateKey)

		if err := storage.Save(data); err != nil {
			log.Fatalf("save server private key: %v", err)
		}

		log.Println("generated new server private key")
	}

	if data.GeneratedParams != nil {
		log.Println("loaded generated AWG params from storage")
	} else {
		gp, err := awg.GenerateParams()
		if err != nil {
			log.Fatalf("generate AWG params: %v", err)
		}

		data.GeneratedParams = gp

		if err := storage.Save(data); err != nil {
			log.Fatalf("save generated AWG params: %v", err)
		}

		log.Printf("generated new AWG params: H1=%s H2=%s H3=%s H4=%s S1=%d S2=%d",
			gp.H1, gp.H2, gp.H3, gp.H4, gp.S1, gp.S2)
	}

	gp := data.GeneratedParams

	defaultParams := awg.AWGParams{
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

	pool, err := awg.NewPool(cfg, privateKey, cfg.MaxInterfaces)
	if err != nil {
		log.Fatalf("create AWG pool: %v", err)
	}

	mgr, err := clients.NewManager(pool, storage, cfg, defaultParams, data)
	if err != nil {
		pool.Close()
		log.Fatalf("create client manager: %v", err)
	}

	collector := usage.NewCollector(cfg.DataDir, pool.InterfaceNames, awg.ShowDump)

	collectorCtx, collectorCancel := context.WithCancel(context.Background())

	go collector.Run(collectorCtx)

	srv := api.NewServer(mgr, cfg, collector)

	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("HTTP server stopped: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh

	log.Printf("received signal %s, shutting down...", sig)

	collectorCancel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	collector.Collect()

	if err := collector.Save(); err != nil {
		log.Printf("warning: failed to save final usage data: %v", err)
	}

	pool.Close()

	log.Println("shutdown complete")
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

	if err := u.Apply(result.DownloadURL); err != nil {
		log.Fatalf("apply update: %v", err)
	}

	fmt.Printf("updated to %s, restart the service to apply\n", result.Latest)
}
