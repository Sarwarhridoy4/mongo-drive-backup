package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type nonceContextKey struct{}

func requestNonce(r *http.Request) string {
	if nonce, ok := r.Context().Value(nonceContextKey{}).(string); ok {
		return nonce
	}
	return newNonce()
}

func newNonce() string {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "fallback-csp-nonce"
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func contentSecurityPolicy(nonce string) string {
	return "default-src 'self'; script-src 'self' 'nonce-" + nonce + "'; style-src 'self' 'nonce-" + nonce + "'; img-src 'self' data:; connect-src 'self' ws: wss:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
}

func addCSPNonce(html, nonce string) string {
	html = strings.Replace(html, "<style>", `<style nonce="`+nonce+`">`, 1)
	return strings.Replace(html, "<script>", `<script nonce="`+nonce+`">`, 1)
}

func (s *Server) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.URL.Path != "/login" {
			s.authMu.RLock()
			authEnabled := s.authUsername != "" || s.authPassword != ""
			s.authMu.RUnlock()
			if authEnabled && !s.authenticated(r) {
				if r.Method == http.MethodGet || r.Method == http.MethodHead {
					http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
				} else {
					http.Error(w, "authentication required", http.StatusUnauthorized)
				}
				return
			}
		}

		if isStateChangingMethod(r.Method) {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != "null" && !sameOrigin(r, origin) {
				s.log.Warn("cross_origin_request_rejected", map[string]interface{}{
					"origin": origin,
					"host":   r.Host,
					"path":   r.URL.Path,
				})
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}

		nonce := newNonce()
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy(nonce))
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceContextKey{}, nonce)))
	})
}

func (s *Server) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie("mongo_drive_session")
	if err != nil || cookie.Value == "" {
		return false
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	expiresAt, ok := s.authSessions[cookie.Value]
	if !ok {
		return false
	}
	if time.Now().After(expiresAt) {
		delete(s.authSessions, cookie.Value)
		return false
	}
	return true
}

func secureEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func isStateChangingMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func requestOrigin(r *http.Request) string {
	scheme := requestScheme(r)
	return scheme + "://" + r.Host
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return "https"
	}
	return "http"
}

func sameOrigin(r *http.Request, origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if !strings.EqualFold(parsed.Scheme, requestScheme(r)) {
		return false
	}
	requestHost := r.Host
	if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
		requestHost = forwardedHost
	}
	if strings.EqualFold(parsed.Host, requestHost) {
		return true
	}

	// Browsers commonly switch between localhost and loopback IPs during local development.
	// Treat those aliases as the same origin only when their ports match.
	if isLoopbackHost(parsed.Hostname()) && isLoopbackHost(hostnameOf(requestHost)) {
		return true
	}
	return false
}

func portOf(host string) string {
	if _, port, err := net.SplitHostPort(host); err == nil {
		return port
	}
	return ""
}

func hostnameOf(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(name, "[]")
	}
	return strings.Trim(host, "[]")
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback()
}
