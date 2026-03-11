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
	ID        string         `json:"id"`
	AWGParams *awg.AWGParams `json:"awg_params,omitempty"`
}

type updateClientRequest struct {
	AWGParams *awg.AWGParams `json:"awg_params"`
}

type clientResponse struct {
	ID        string         `json:"id"`
	Address   string         `json:"address"`
	CreatedAt string         `json:"created_at"`
	AWGParams *awg.AWGParams `json:"awg_params,omitempty"`
}

func toResponse(c clients.ClientData) clientResponse {
	return clientResponse{
		ID:        c.ID,
		Address:   c.Address,
		CreatedAt: c.CreatedAt,
		AWGParams: c.AWGParams,
	}
}

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request) {
	cls := s.manager.ListClients()

	result := make([]clientResponse, 0, len(cls))

	for _, c := range cls {
		result = append(result, toResponse(c))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req createClientRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		jsonError(w, "id is required", http.StatusBadRequest)
		return
	}

	if len(req.ID) > 256 {
		jsonError(w, "id is too long (max 256 chars)", http.StatusBadRequest)
		return
	}

	client, err := s.manager.CreateClient(req.ID, req.AWGParams)
	if err != nil {
		log.Printf("create client error: %v", err)

		var status int

		switch {
		case errors.Is(err, clients.ErrClientExists):
			status = http.StatusConflict
		case errors.Is(err, awg.ErrPortInUse):
			status = http.StatusConflict
		case errors.Is(err, awg.ErrMaxInterfacesReached):
			status = http.StatusServiceUnavailable
		default:
			status = http.StatusInternalServerError
		}

		writeError(w, err, status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toResponse(*client))
}

func (s *Server) handleUpdateClient(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	id := r.PathValue("id")

	var req updateClientRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	client, err := s.manager.UpdateClient(id, req.AWGParams)
	if err != nil {
		log.Printf("update client error: %v", err)

		var status int

		switch {
		case errors.Is(err, clients.ErrClientNotFound):
			status = http.StatusNotFound
		case errors.Is(err, awg.ErrPortInUse):
			status = http.StatusConflict
		case errors.Is(err, awg.ErrPortShared):
			status = http.StatusConflict
		case errors.Is(err, awg.ErrMaxInterfacesReached):
			status = http.StatusServiceUnavailable
		default:
			status = http.StatusInternalServerError
		}

		writeError(w, err, status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toResponse(*client))
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
