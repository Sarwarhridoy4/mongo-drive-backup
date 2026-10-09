package web

import (
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.authMu.RLock()
	authEnabled := s.authUsername != "" || s.authPassword != ""
	s.authMu.RUnlock()
	if !authEnabled {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	next := safeNext(r.URL.Query().Get("next"))
	if r.Method == http.MethodGet {
		s.writeLoginPage(w, next, "")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.writeLoginPage(w, next, "Invalid form submission.")
		return
	}

	s.authMu.RLock()
	username, password := s.authUsername, s.authPassword
	s.authMu.RUnlock()
	if !secureEqual(r.FormValue("username"), username) || !secureEqual(r.FormValue("password"), password) {
		s.writeLoginPage(w, next, "Incorrect username or password.")
		return
	}

	token := newNonce()
	s.authMu.Lock()
	s.authSessions[token] = time.Now().Add(12 * time.Hour)
	s.authMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "mongo_drive_session",
		Value:    token,
		Path:     "/",
		MaxAge:   12 * 60 * 60,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie("mongo_drive_session"); err == nil {
		s.authMu.Lock()
		delete(s.authSessions, cookie.Value)
		s.authMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "mongo_drive_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func safeNext(next string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		return next
	}
	return "/"
}

func (s *Server) writeLoginPage(w http.ResponseWriter, next, errorMessage string) {
	errorHTML := ""
	if errorMessage != "" {
		errorHTML = `<div class="error">` + errorMessage + `</div>`
	}
	html := `<!doctype html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Sign in · MongoDrive</title><link rel="icon" type="image/svg+xml" href="/favicon.svg"><style>
:root{color-scheme:dark;--bg:#07111f;--panel:#0d1b2e;--line:#223754;--text:#e6f0f8;--muted:#9bb0c5;--accent:#6ee7b7;--danger:#fecdd3}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:radial-gradient(circle at 20% 0,#122a3b 0,transparent 42%),var(--bg);font-family:Inter,"Segoe UI",sans-serif;color:var(--text)}.card{width:min(100%,400px);padding:32px;border:1px solid rgba(145,175,204,.2);border-radius:22px;background:rgba(13,27,46,.94);box-shadow:0 24px 70px #0005}.brand{display:flex;align-items:center;gap:12px;margin-bottom:28px}.mark{display:block;width:42px;height:42px;border-radius:12px;object-fit:cover;box-shadow:0 8px 24px rgba(52,211,153,.24)}.eyebrow{margin:0 0 6px;color:var(--accent);font-size:12px;font-weight:800;letter-spacing:.12em;text-transform:uppercase}.title{margin:0;font-size:26px}.copy{color:var(--muted);line-height:1.5;margin:10px 0 24px}label{display:block;margin:16px 0 7px;color:var(--muted);font-size:13px;font-weight:700}input{width:100%;padding:12px 13px;border:1px solid var(--line);border-radius:10px;background:#081523;color:var(--text);font:inherit}input:focus{outline:2px solid var(--accent);outline-offset:2px}button{width:100%;margin-top:24px;padding:12px;border:0;border-radius:10px;background:var(--accent);color:#06221d;font:inherit;font-weight:800;cursor:pointer}.error{padding:10px 12px;border:1px solid #7f3345;border-radius:10px;background:#351d29;color:var(--danger);font-size:14px}</style></head><body><main class="card"><div class="brand"><img class="mark" src="/logo.svg" alt="MongoDrive Backup" width="42" height="42"><div><p class="eyebrow">MongoDrive</p><h1 class="title">Welcome back</h1></div></div><p class="copy">Sign in to access the backup dashboard.</p>` + errorHTML + `<form method="post" action="/login"><input type="hidden" name="next" value="` + htmlEscape(next) + `"><label for="username">Username</label><input id="username" name="username" type="text" autocomplete="username" required autofocus><label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password" required><button type="submit">Sign in</button></form></main></body></html>`
	nonce := requestNonce(&http.Request{})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy(nonce))
	_, _ = w.Write([]byte(addCSPNonce(html, nonce)))
}

func htmlEscape(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "\"", "&quot;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	return strings.ReplaceAll(value, ">", "&gt;")
}
