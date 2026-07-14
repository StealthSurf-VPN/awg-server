package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/clients"
)

type optionalField[T any] struct {
	Set   bool
	Value *T
}

func (f *optionalField[T]) UnmarshalJSON(data []byte) error {
	f.Set = true

	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.Value = nil
		return nil
	}

	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}

	f.Value = &value
	return nil
}

type createClientRequest struct {
	ID        string           `json:"id"`
	AWGParams *awg.AWGParams   `json:"awg_params,omitempty"`
	Routing   *clients.Routing `json:"routing,omitempty"`
}

type updateClientRequest struct {
	AWGParams optionalField[awg.AWGParams]   `json:"awg_params"`
	Routing   optionalField[clients.Routing] `json:"routing"`
}

type clientResponse struct {
	ID        string          `json:"id"`
	Address   string          `json:"address"`
	CreatedAt string          `json:"created_at"`
	AWGParams *awg.AWGParams  `json:"awg_params,omitempty"`
	Routing   clients.Routing `json:"routing"`
}

func toResponse(c clients.ClientData) clientResponse {
	return clientResponse{
		ID:        c.ID,
		Address:   c.Address,
		CreatedAt: c.CreatedAt,
		AWGParams: c.AWGParams,
		Routing:   clients.EffectiveRouting(c.Routing),
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

func (s *Server) handleGenerateAWGParams(w http.ResponseWriter, r *http.Request) {
	params, err := awg.GenerateParams()
	if err != nil {
		log.Printf("generate awg params error: %v", err)
		writeError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(params)
}

func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req createClientRequest

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		jsonError(w, "id is required", http.StatusBadRequest)
		return
	}

	if utf8.RuneCountInString(req.ID) > 256 {
		jsonError(w, "id is too long (max 256 chars)", http.StatusBadRequest)
		return
	}

	normalizedParams, err := awg.NormalizeOverrides(req.AWGParams)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	req.AWGParams = normalizedParams

	normalizedRouting, err := clients.NormalizeRouting(req.Routing)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	client, err := s.manager.CreateClient(req.ID, req.AWGParams, normalizedRouting)
	if err != nil {
		log.Printf("create client error: %v", err)

		var status int

		switch {
		case errors.Is(err, awg.ErrRollbackFailed):
			status = http.StatusInternalServerError
		case errors.Is(err, clients.ErrClientExists):
			status = http.StatusConflict
		case errors.Is(err, awg.ErrInvalidParams):
			status = http.StatusBadRequest
		case errors.Is(err, awg.ErrInvalidPort):
			status = http.StatusBadRequest
		case errors.Is(err, clients.ErrInvalidRouting):
			status = http.StatusBadRequest
		case errors.Is(err, awg.ErrPortInUse):
			status = http.StatusConflict
		case errors.Is(err, awg.ErrProfilePortConflict):
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

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if !req.AWGParams.Set && !req.Routing.Set {
		jsonError(w, clients.ErrEmptyClientUpdate.Error(), http.StatusBadRequest)
		return
	}

	if req.AWGParams.Set {
		normalizedParams, err := awg.NormalizeOverrides(req.AWGParams.Value)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		req.AWGParams.Value = normalizedParams
	}

	if req.Routing.Set {
		normalizedRouting, err := clients.NormalizeRouting(req.Routing.Value)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}

		req.Routing.Value = normalizedRouting
	}

	client, err := s.manager.UpdateClient(id, clients.ClientUpdate{
		AWGParams:    req.AWGParams.Value,
		AWGParamsSet: req.AWGParams.Set,
		Routing:      req.Routing.Value,
		RoutingSet:   req.Routing.Set,
	}, s.collector.WithRequiredSnapshot)
	if err != nil {
		log.Printf("update client error: %v", err)
		writeError(w, err, clientUpdateErrorStatus(err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toResponse(*client))
}

func (s *Server) handleRegenerateClientAWGParams(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := s.manager.RegenerateAWGParams(id, s.collector.WithRequiredSnapshot)
	if err != nil {
		status := clientUpdateErrorStatus(err)

		if status == http.StatusInternalServerError {
			log.Printf("client operation failed: operation=regenerate_awg_params client_id=%q status=%d error=%v", id, status, err)
		} else {
			log.Printf("client operation failed: operation=regenerate_awg_params client_id=%q status=%d", id, status)
		}

		writeError(w, err, status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toResponse(*client))
}

func clientUpdateErrorStatus(err error) int {
	switch {
	case errors.Is(err, awg.ErrRollbackFailed):
		return http.StatusInternalServerError
	case errors.Is(err, clients.ErrClientNotFound):
		return http.StatusNotFound
	case errors.Is(err, awg.ErrInvalidParams):
		return http.StatusBadRequest
	case errors.Is(err, awg.ErrInvalidPort):
		return http.StatusBadRequest
	case errors.Is(err, clients.ErrInvalidRouting):
		return http.StatusBadRequest
	case errors.Is(err, clients.ErrEmptyClientUpdate):
		return http.StatusBadRequest
	case errors.Is(err, awg.ErrPortInUse):
		return http.StatusConflict
	case errors.Is(err, awg.ErrPortShared):
		return http.StatusConflict
	case errors.Is(err, awg.ErrProfilePortConflict):
		return http.StatusConflict
	case errors.Is(err, awg.ErrMaxInterfacesReached):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
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

type statsResponse struct {
	RxBytes       int64  `json:"rx_bytes"`
	TxBytes       int64  `json:"tx_bytes"`
	LastHandshake string `json:"last_handshake,omitempty"`
}

func (s *Server) handleGetClientStats(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := s.manager.GetClient(id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, clients.ErrClientNotFound) {
			status = http.StatusNotFound
		}

		writeError(w, err, status)
		return
	}

	stats, _ := s.collector.GetStats(client.PublicKey)

	resp := statsResponse{
		RxBytes: stats.TotalRx,
		TxBytes: stats.TotalTx,
	}

	if !stats.LastHandshake.IsZero() {
		resp.LastHandshake = stats.LastHandshake.UTC().Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	client, err := s.manager.GetClient(id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, clients.ErrClientNotFound) {
			status = http.StatusNotFound
		}

		writeError(w, err, status)
		return
	}

	if err := s.manager.DeleteClient(id); err != nil {
		log.Printf("delete client error: %v", err)

		status := http.StatusInternalServerError
		if errors.Is(err, clients.ErrClientNotFound) {
			status = http.StatusNotFound
		}

		writeError(w, err, status)
		return
	}

	s.collector.RemoveStats(client.PublicKey)

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
