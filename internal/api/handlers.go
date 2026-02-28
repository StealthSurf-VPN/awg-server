package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/stealthsurf-vpn/awg-server/internal/clients"
)

type createClientRequest struct {
	Name string `json:"name"`
}

type clientResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request) {
	clients := s.manager.ListClients()

	result := make([]clientResponse, 0, len(clients))

	for _, c := range clients {
		result = append(result, clientResponse{
			ID:      c.ID,
			Name:    c.Name,
			Address: c.Address,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	var req createClientRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}

	client, err := s.manager.CreateClient(req.Name)
	if err != nil {
		log.Printf("create client error: %v", err)

		status := http.StatusInternalServerError
		if errors.Is(err, clients.ErrClientExists) {
			status = http.StatusConflict
		}

		jsonError(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(clientResponse{
		ID:      client.ID,
		Name:    client.Name,
		Address: client.Address,
	})
}

func (s *Server) handleGetConfiguration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	cfg, err := s.manager.GetClientConfig(id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, clients.ErrClientNotFound) {
			status = http.StatusNotFound
		}

		jsonError(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(cfg))
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := s.manager.DeleteClient(id); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, clients.ErrClientNotFound) {
			status = http.StatusNotFound
		}

		jsonError(w, err.Error(), status)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
