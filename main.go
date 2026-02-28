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

	dev, err := awg.NewDevice(cfg, privateKey)
	if err != nil {
		log.Fatalf("create AWG device: %v", err)
	}

	mgr, err := clients.NewManager(dev, storage, cfg)
	if err != nil {
		dev.Close()
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

	dev.Close()

	log.Println("shutdown complete")
}
