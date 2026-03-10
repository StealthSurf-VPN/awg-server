package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/clients"
)

type createClientRequest struct {
	Name      string         `json:"name"`
	AWGParams *awg.AWGParams `json:"awg_params,omitempty"`
}

type updateClientRequest struct {
	AWGParams *awg.AWGParams `json:"awg_params"`
}

type clientResponse struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Address   string         `json:"address"`
	CreatedAt string         `json:"created_at"`
	AWGParams *awg.AWGParams `json:"awg_params,omitempty"`
}

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request) {
	cls := s.manager.ListClients()

	result := make([]clientResponse, 0, len(cls))

	for _, c := range cls {
		result = append(result, clientResponse{
			ID:        c.ID,
			Name:      c.Name,
			Address:   c.Address,
			CreatedAt: c.CreatedAt,
			AWGParams: c.AWGParams,
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

	if len(req.Name) > 256 {
		jsonError(w, "name is too long (max 256 chars)", http.StatusBadRequest)
		return
	}

	client, err := s.manager.CreateClient(req.Name, req.AWGParams)
	if err != nil {
		log.Printf("create client error: %v", err)

		status := http.StatusInternalServerError
		if errors.Is(err, clients.ErrClientExists) {
			status = http.StatusConflict
		}
		if errors.Is(err, awg.ErrMaxInterfacesReached) {
			status = http.StatusServiceUnavailable
		}
		if errors.Is(err, awg.ErrPortInUse) {
			status = http.StatusConflict
		}

		writeError(w, err, status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(clientResponse{
		ID:        client.ID,
		Name:      client.Name,
		Address:   client.Address,
		CreatedAt: client.CreatedAt,
		AWGParams: client.AWGParams,
	})
}

func (s *Server) handleUpdateClient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req updateClientRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	client, err := s.manager.UpdateClient(id, req.AWGParams)
	if err != nil {
		log.Printf("update client error: %v", err)

		status := http.StatusInternalServerError
		if errors.Is(err, clients.ErrClientNotFound) {
			status = http.StatusNotFound
		}
		if errors.Is(err, awg.ErrMaxInterfacesReached) {
			status = http.StatusServiceUnavailable
		}
		if errors.Is(err, awg.ErrPortInUse) {
			status = http.StatusConflict
		}

		writeError(w, err, status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clientResponse{
		ID:        client.ID,
		Name:      client.Name,
		Address:   client.Address,
		CreatedAt: client.CreatedAt,
		AWGParams: client.AWGParams,
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

		writeError(w, err, status)
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

		writeError(w, err, status)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func writeError(w http.ResponseWriter, err error, status int) {
	if status == http.StatusInternalServerError {
		jsonError(w, "internal server error", status)
		return
	}

	jsonError(w, err.Error(), status)
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
