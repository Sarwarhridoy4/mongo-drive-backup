package web

import (
	"context"
	"encoding/json"
	"net/http"
)

type RestoreRequest struct {
	FileID       string `json:"file_id"`
	MongoURI     string `json:"mongo_uri"`
	Database     string `json:"database"`
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.restoreHandler == nil {
		http.Error(w, "restore is not configured", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	var request RestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid restore request", http.StatusBadRequest)
		return
	}
	if err := s.restoreHandler(context.Background(), request); err != nil {
		s.log.Error("web_restore_failed", map[string]interface{}{"error": err.Error(), "database": request.Database})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "restore completed"})
}
