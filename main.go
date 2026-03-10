package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stealthsurf-vpn/awg-server/internal/api"
	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/clients"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

func main() {
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

	defaultParams := awg.AWGParams{
		Jc:   cfg.Jc,
		Jmin: cfg.Jmin,
		Jmax: cfg.Jmax,
		S1:   cfg.S1,
		S2:   cfg.S2,
		S3:   cfg.S3,
		S4:   cfg.S4,
		H1:   cfg.H1,
		H2:   cfg.H2,
		H3:   cfg.H3,
		H4:   cfg.H4,
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

	srv := api.NewServer(mgr, cfg)

	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("HTTP server stopped: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh

	log.Printf("received signal %s, shutting down...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	pool.Close()

	log.Println("shutdown complete")
}
