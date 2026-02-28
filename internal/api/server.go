package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/stealthsurf-vpn/awg-server/internal/clients"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

type Server struct {
	manager    *clients.Manager
	config     *config.Config
	httpServer *http.Server
}

func NewServer(manager *clients.Manager, cfg *config.Config) *Server {
	s := &Server{
		manager: manager,
		config:  cfg,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/clients", s.authMiddleware(s.handleListClients))
	mux.HandleFunc("POST /api/clients", s.authMiddleware(s.handleCreateClient))
	mux.HandleFunc("GET /api/clients/{id}/configuration", s.authMiddleware(s.handleGetConfiguration))
	mux.HandleFunc("DELETE /api/clients/{id}", s.authMiddleware(s.handleDeleteClient))

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

func (s *Server) Start() error {
	log.Printf("HTTP API server listening on :%d", s.config.HTTPPort)
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")

		if authHeader == "" {
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")

		if token == authHeader || token != s.config.APIToken {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}
