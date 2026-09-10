package web

import (
	"context"
	"encoding/json"
	"net/http"

	"golang.org/x/oauth2"
)

func (s *Server) handleOAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	configured := s.oauthHandler != nil
	authorized := configured && s.oauthHandler.HasValidToken()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{
		"configured": configured,
		"authorized": authorized,
	})
}

func (s *Server) handleOAuth2Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("missing code"))
		return
	}

	select {
	case s.oauthCodeCh <- code:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OAuth authorization successful. You can close this tab."))
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("oauth callback not ready"))
	}
}

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.oauthHandler == nil {
		http.Error(w, "oauth not configured", http.StatusServiceUnavailable)
		return
	}
	if s.oauthHandler.HasValidToken() {
		http.Error(w, "oauth already authorized", http.StatusConflict)
		return
	}

	url, config, err := s.oauthHandler.GetAuthURL(r.Context())
	if err != nil {
		s.log.Error("oauth_auth_url_failed", map[string]interface{}{
			"error": err.Error(),
		})
		http.Error(w, "failed to generate auth url", http.StatusInternalServerError)
		return
	}

	s.SetOAuthAuthURL(url)

	go func(ctx context.Context, cfg *oauth2.Config) {
		tok, err := s.oauthHandler.WaitForToken(ctx, cfg)
		if err != nil {
			s.log.Error("oauth_token_exchange_failed", map[string]interface{}{
				"error": err.Error(),
			})
			return
		}
		if err := s.oauthHandler.SaveToken(tok); err != nil {
			s.log.Error("oauth_token_save_failed", map[string]interface{}{
				"error": err.Error(),
			})
			return
		}
		s.log.Info("oauth_token_saved_via_ui", nil)
	}(context.Background(), config)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": url})
}
