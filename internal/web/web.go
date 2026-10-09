package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/websocket"
	driveapi "google.golang.org/api/drive/v3"

	"github.com/sarwar/mongo-drive-backup/internal/drive"
	"github.com/sarwar/mongo-drive-backup/internal/logger"
)

type Server struct {
	port           string
	status         Status
	statusMu       sync.RWMutex
	triggerCh      chan struct{}
	log            *logger.Logger
	lastRunMu      sync.RWMutex
	lastRunTime    time.Time
	oauthCodeCh    chan string
	stopCh         chan struct{}
	restartCh      chan struct{}
	logs           []logger.Entry
	logsMu         sync.RWMutex
	listBackups    func() ([]*driveapi.File, error)
	oauthHandler   *drive.OAuth2Uploader
	oauthAuthURL   string
	oauthMu        sync.RWMutex
	authMu         sync.RWMutex
	authUsername   string
	authPassword   string
	authSessions   map[string]time.Time
	wsMu           sync.RWMutex
	wsConns        map[*websocket.Conn]struct{}
	nextRunGetter  func() (time.Time, bool)
	restoreHandler func(context.Context, RestoreRequest) error
	httpMu         sync.RWMutex
	httpServer     *http.Server
}

func NewServer(port string, log *logger.Logger) *Server {
	s := &Server{
		port:        port,
		log:         log,
		triggerCh:   make(chan struct{}, 1),
		oauthCodeCh: make(chan string, 1),
		stopCh:      make(chan struct{}),
		restartCh:   make(chan struct{}),
		logs:        make([]logger.Entry, 0, 200),
		status: Status{
			Environment: "production",
			Schedule:    "0 2 * * *",
			Timezone:    "UTC",
			LastStatus:  "idle",
		},
		wsConns:      make(map[*websocket.Conn]struct{}),
		authSessions: make(map[string]time.Time),
	}
	log.AddHook(s.appendLog)
	return s
}

func (s *Server) SetOAuthHandler(handler *drive.OAuth2Uploader) {
	s.oauthHandler = handler
}

func (s *Server) SetOAuthAuthURL(url string) {
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	s.oauthAuthURL = url
}

func (s *Server) OAuthAuthURL() string {
	s.oauthMu.RLock()
	defer s.oauthMu.RUnlock()
	return s.oauthAuthURL
}

// SetBasicAuth configures the dashboard login credentials.
// It is intentionally opt-in so local development remains frictionless.
func (s *Server) SetBasicAuth(username, password string) {
	s.authMu.Lock()
	s.authUsername = username
	s.authPassword = password
	s.authMu.Unlock()
}

func (s *Server) SetRestoreHandler(handler func(context.Context, RestoreRequest) error) {
	s.restoreHandler = handler
}

func (s *Server) Trigger() <-chan struct{} {
	return s.triggerCh
}

func (s *Server) OAuthCodeCh() <-chan string {
	return s.oauthCodeCh
}

func (s *Server) StopCh() <-chan struct{} {
	return s.stopCh
}

func (s *Server) RestartCh() <-chan struct{} {
	return s.restartCh
}

//go:embed assets/favicon.svg
var faviconSVG []byte

//go:embed assets/logo.svg
var logoSVG []byte

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(faviconSVG)
}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(logoSVG)
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/favicon.svg", s.handleFavicon)
	mux.HandleFunc("/logo.svg", s.handleLogo)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/backup/now", s.handleBackupNow)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/restart", s.handleRestart)
	mux.HandleFunc("/api/restore", s.handleRestore)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/backups", s.handleBackups)
	mux.HandleFunc("/api/oauth/status", s.handleOAuthStatus)
	mux.HandleFunc("/api/oauth/start", s.handleOAuthStart)
	mux.HandleFunc("/oauth2callback", s.handleOAuth2Callback)
	mux.Handle("/ws", websocket.Handler(s.handleWebSocket))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/metrics", s.handleMetrics)

	s.log.Info("web_server_started", map[string]interface{}{
		"port": s.port,
	})

	httpServer := &http.Server{Addr: ":" + s.port, Handler: s.secureHeaders(mux)}
	s.httpMu.Lock()
	s.httpServer = httpServer
	s.httpMu.Unlock()

	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen on port %s: %w", s.port, err)
	}
	return nil
}

// Shutdown stops the dashboard listener and allows active requests to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	s.httpMu.RLock()
	httpServer := s.httpServer
	s.httpMu.RUnlock()
	if httpServer == nil {
		return nil
	}
	return httpServer.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	html := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>MongoDB Backup Service</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg" />
<style>
  :root {
    --deep: #07111f;
    --deep-2: #0b1728;
    --green: #6ee7b7;
    --green-soft: #d1fae5;
    --green-muted: #7c93ad;
    --green-deep: #123a38;
    --line: #223754;
    --line-soft: #31516d;
    --panel: #0d1b2e;
    --panel-soft: #12243a;
    --panel-bg: rgba(13, 27, 46, 0.86);
    --border: rgba(145, 175, 204, 0.18);
    --text: #e6f0f8;
    --muted: #9bb0c5;
    --subtle: #71879e;
    --primary: var(--green);
    --primary-2: var(--green-soft);
  }

  * {
    box-sizing: border-box;
  }

  body {
    margin: 0;
    font-family: Inter, "Segoe UI", Roboto, Arial, sans-serif;
    background: radial-gradient(circle at 15% 0%, #122a3b 0, transparent 34%), var(--deep);
    color: var(--text);
    min-height: 100vh;
    letter-spacing: 0.01em;
  }

  .dashboard-wrap {
    max-width: 1540px;
    margin: 0 auto;
    padding: 32px 24px 48px;
  }

  .dashboard-shell {
    display: flex;
    gap: 24px;
    min-height: calc(100vh - 80px);
  }

  .dashboard-grid {
    display: grid;
    grid-template-columns: minmax(280px, 0.95fr) minmax(420px, 1.45fr);
    gap: 20px;
  }

  .sidebar {
    width: 252px;
    background: var(--panel-bg);
    border: 1px solid var(--border);
    border-radius: 20px;
    padding: 20px 14px;
    box-shadow: 0 20px 50px rgba(0,0,0,0.22);
    flex-shrink: 0;
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 4px 0 20px;
  }

  .brand-mark {
    width: 42px;
    height: 42px;
    border-radius: 12px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(135deg, #a7f3d0, #34d399);
    color: #06221d;
    font-weight: 900;
    font-size: 1.1rem;
    box-shadow: 0 8px 24px rgba(52, 211, 153, 0.24);
  }

  .brand-name {
    font-size: 1.04rem;
    font-weight: 800;
    color: var(--text);
  }

  .brand-sub {
    color: var(--muted);
    font-size: 0.76rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .nav-list {
    margin: 30px 0 20px;
    padding: 0;
    list-style: none;
  }

  .nav-list li {
    margin-bottom: 8px;
  }

  .nav-link {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--muted);
    font-size: 0.86rem;
    padding: 11px 12px;
    border-radius: 10px;
    text-decoration: none;
    border: 1px solid transparent;
  }

  .nav-link.active,
  .nav-link:hover {
    color: var(--text);
    border-color: rgba(110, 231, 183, 0.3);
    background: linear-gradient(90deg, rgba(110, 231, 183, 0.13), transparent);
  }

  .nav-link .nav-icon {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--subtle);
  }

  .nav-link.active .nav-icon,
  .nav-link:hover .nav-icon {
    background: var(--green);
    box-shadow: 0 0 0 4px rgba(110, 231, 183, 0.12);
  }

  .sidebar-card {
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 12px;
    background: rgba(18, 36, 58, 0.7);
    color: var(--muted);
    font-size: 0.78rem;
    margin-top: 14px;
  }

  .sidebar-card strong {
    color: var(--text);
  }

  .sign-out {
    color: var(--muted);
    font-size: 0.78rem;
    text-decoration: none;
  }

  .sign-out-wrap {
    margin-top: 10px;
  }

  .sign-out:hover {
    color: var(--green-soft);
    text-decoration: underline;
  }

  .main-panel {
    flex: 1;
    min-width: 0;
  }

  .topbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 20px;
    background: var(--panel-bg);
    border: 1px solid var(--border);
    border-radius: 20px;
    padding: 22px 24px;
    box-shadow: 0 8px 24px rgba(0,0,0,0.2);
  }

  .dashboard-title {
    margin: 0 0 4px;
    font-size: clamp(1.7rem, 2.5vw, 2.4rem);
    font-weight: 750;
    line-height: 1.2;
  }

  .dashboard-subtitle {
    margin: 0;
    color: var(--muted);
    font-size: 0.9rem;
  }

  .service-chip {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    border-radius: 999px;
    border: 1px solid rgba(110, 231, 183, 0.22);
    background: rgba(110, 231, 183, 0.08);
    font-size: 0.86rem;
    color: var(--muted);
    white-space: nowrap;
  }

  .topbar-actions {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .timezone-button {
    padding: 8px 12px;
    font-size: 0.8rem;
  }

  .service-chip::before {
    content: "";
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--green);
    box-shadow: 0 0 0 4px rgba(120, 255, 154, 0.16);
  }

  .panel {
    background: linear-gradient(145deg, rgba(13, 27, 46, 0.96), rgba(10, 23, 39, 0.9));
    border: 1px solid var(--border);
    border-radius: 20px;
    padding: 22px;
    margin-bottom: 20px;
    box-shadow: 0 18px 45px rgba(0,0,0,0.18);
  }

  .stats-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(180px, 1fr));
    gap: 14px;
  }

  .stat-card {
    background: var(--panel-soft);
    border: 1px solid var(--border);
    border-radius: 14px;
    padding: 16px;
    min-height: 96px;
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .stat-label {
    color: var(--subtle);
    font-size: 0.78rem;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .stat-value {
    margin-top: 8px;
    font-weight: 700;
    font-size: 1rem;
    color: var(--text);
    font-variant-numeric: tabular-nums;
    word-break: break-word;
  }

  .stat-value.error {
    color: var(--green-soft);
  }

  .stat-value.success {
    color: var(--green-soft);
  }

  .stat-value.running {
    color: var(--green);
  }

  .actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 12px;
    margin: 0 0 16px;
  }

  .action-button {
    appearance: none;
    border: 0;
    color: var(--deep);
    padding: 11px 16px;
    border-radius: 10px;
    font-weight: 800;
    font-size: 0.9rem;
    cursor: pointer;
    transition: transform 180ms ease, filter 180ms ease, opacity 180ms ease, box-shadow 180ms ease;
    background: var(--green);
    box-shadow: 0 8px 18px rgba(52, 211, 153, 0.14);
  }

  .action-button:hover {
    filter: brightness(1.08);
    transform: translateY(-1px);
  }

  .action-button:focus-visible {
    outline: 2px solid var(--green-soft);
    outline-offset: 3px;
  }

  .action-button:disabled {
    cursor: not-allowed;
    opacity: 0.78;
    filter: grayscale(0.4);
  }

  .action-button.run {
    background: var(--green);
  }

  .action-button.restart {
    background: #203651;
    color: var(--text);
    box-shadow: none;
  }

  .action-button.stop {
    background: #4b2630;
    color: #fecdd3;
    box-shadow: none;
  }

  .action-button.authorize {
    background: #b8f5dd;
  }

  .message {
    color: var(--muted);
    font-size: 0.86rem;
  }

  .restore-form {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 14px;
  }

  .restore-form label {
    display: block;
    color: var(--subtle);
    font-size: 0.78rem;
    font-weight: 700;
    margin-bottom: 6px;
  }

  .restore-form input,
  .restore-form select {
    width: 100%;
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 11px 12px;
    background: #081523;
    color: var(--text);
    font: inherit;
  }

  .secret-input {
    display: flex;
    align-items: stretch;
  }

  .secret-input input {
    min-width: 0;
    border-radius: 10px 0 0 10px;
  }

  .secret-toggle {
    border: 1px solid var(--border);
    border-left: 0;
    border-radius: 0 10px 10px 0;
    padding: 0 12px;
    background: #102235;
    color: var(--subtle);
    cursor: pointer;
    font: inherit;
  }

  .secret-toggle:hover,
  .secret-toggle:focus-visible {
    color: var(--text);
    outline: none;
  }

  .restore-form .full {
    grid-column: 1 / -1;
  }

  .restore-warning {
    grid-column: 1 / -1;
    margin: 0;
    color: #fecdd3;
    font-size: 0.82rem;
  }

  .panel-title {
    margin: 0 0 14px;
    font-size: clamp(1.2rem, 3vw, 1.4rem);
    font-weight: 700;
  }

  .backup-list {
    display: flex;
    flex-direction: column;
    gap: 10px;
    max-height: 320px;
    overflow-y: auto;
    padding-right: 4px;
  }

  .backup-item {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    color: var(--text);
    font-size: 0.86rem;
    word-break: break-all;
    border-bottom: 1px solid var(--border);
    padding: 10px 0;
  }

  .backup-item:last-child {
    border-bottom: 0;
    padding-bottom: 0;
  }

  .backup-item a {
    color: var(--green-soft);
    text-decoration: none;
  }

  .backup-item a:hover {
    text-decoration: underline;
  }

  .backup-meta {
    color: var(--muted);
    font-size: 0.8rem;
  }

  .terminal-panel {
    background: #091523;
    border-color: var(--border);
  }

  .terminal-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 0 0 12px;
    border-bottom: 1px solid var(--border);
  }

  .terminal-title {
    font-size: 0.84rem;
    font-weight: 800;
    letter-spacing: 0.12em;
    color: var(--green-soft);
    text-transform: uppercase;
  }

  .terminal-buttons {
    display: flex;
    gap: 6px;
  }

  .terminal-button {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: var(--green-muted);
  }

  .terminal-button.red {
    background: var(--green-soft);
  }

  .terminal-button.amber {
    background: var(--green);
  }

  .terminal-button.green {
    background: var(--green-soft);
  }

  .log-list {
    font-family: "JetBrains Mono", "Roboto Mono", monospace;
    font-size: 0.82rem;
    color: var(--text);
    background: var(--deep);
    border-radius: 10px;
    border: 1px solid var(--border);
    padding: 14px;
    white-space: pre-wrap;
    overflow-y: auto;
    overflow-x: hidden;
    word-break: break-word;
    overflow-wrap: break-word;
    max-height: 270px;
    line-height: 1.65;
    box-shadow: inset 0 0 24px rgba(0, 0, 0, 0.24);
    background-image: linear-gradient(180deg, rgba(110, 231, 183, 0.025), transparent 30%);
  }

  .log-line {
    display: block;
    border-bottom: 1px dotted rgba(145, 175, 204, 0.2);
    padding: 2px 0;
  }

  .log-line:last-child {
    border-bottom: 0;
    padding-bottom: 0;
  }

  .log-line::before {
    content: "$ ";
    color: var(--green);
    font-weight: 800;
  }

  .log-level {
    color: var(--green-soft);
    font-weight: 700;
  }

  @media (max-width: 1100px) {
    .dashboard-shell {
      flex-direction: column;
    }

    .sidebar {
      width: 100%;
    }

    .nav-list {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      margin: 16px 0;
    }

    .nav-list li {
      margin: 0;
    }

    .dashboard-grid {
      grid-template-columns: 1fr;
    }

    .stats-grid {
      grid-template-columns: repeat(2, minmax(200px, 1fr));
    }
  }

  @media (max-width: 768px) {
    .dashboard-wrap {
      padding: 12px 10px 24px;
    }

    .dashboard-shell {
      min-height: auto;
    }

    .stats-grid {
      grid-template-columns: 1fr;
    }

    .actions {
      align-items: stretch;
      flex-direction: column;
    }

    .action-button {
      width: 100%;
      flex: 1 1 100%;
    }

    .panel {
      padding: 16px;
    }

    .topbar {
      align-items: flex-start;
      flex-direction: column;
    }

    .dashboard-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 520px) {
    .dashboard-wrap {
      padding: 8px 8px 20px;
    }

    .panel {
      padding: 14px;
    }

    .action-button {
      flex-basis: 100%;
    }

    .log-list {
      max-height: 240px;
    }
  }
</style>
</head>
<body>
  <div class="dashboard-wrap">
  <div class="dashboard-shell">
    <aside class="sidebar">
      <div class="brand">
        <img class="brand-mark" src="/logo.svg" alt="MongoDrive Backup" width="44" height="44" />
        <div>
          <div class="brand-name">MongoDrive</div>
          <div class="brand-sub">Backup</div>
        </div>
      </div>

      <ul class="nav-list">
        <li><a class="nav-link active" href="#"><span class="nav-icon"></span>Overview</a></li>
        <li><a class="nav-link" href="#"><span class="nav-icon"></span>Backups</a></li>
        <li><a class="nav-link" href="#"><span class="nav-icon"></span>Log Stream</a></li>
        <li><a class="nav-link" href="#"><span class="nav-icon"></span>Drive Auth</a></li>
      </ul>

      <div class="sidebar-card">
        <div><strong>Service:</strong> MongoDB</div>
        <div><strong>Target:</strong> Google Drive</div>
        <div class="sign-out-wrap"><a class="sign-out" href="/logout">Sign out</a></div>
      </div>
    </aside>

    <main class="main-panel">
      <section class="topbar">
        <div>
          <h1 class="dashboard-title">MongoDB Google Drive Backup</h1>
          <p class="dashboard-subtitle">Service dashboard</p>
        </div>
        <div class="topbar-actions">
          <button id="tzToggle" class="action-button timezone-button">UTC</button>
          <div class="service-chip">online</div>
        </div>
      </section>

      <section class="panel">
        <div class="stats-grid">
          <article class="stat-card">
            <div class="stat-label">Environment</div>
            <div id="env" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Database</div>
            <div id="database" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Schedule</div>
            <div id="schedule" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Timezone</div>
            <div id="timezone" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Current Time</div>
            <div id="current_time" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Next Run</div>
            <div id="next_run" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Next auto backup starts in</div>
            <div id="countdown" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Last Backup</div>
            <div id="last_backup" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Status</div>
            <div id="last_status" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Last File</div>
            <div id="last_file" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Last Size</div>
            <div id="last_size" class="stat-value">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Last Error</div>
            <div id="last_error" class="stat-value error">-</div>
          </article>
          <article class="stat-card">
            <div class="stat-label">Progress</div>
            <div id="progress" class="stat-value">-</div>
          </article>
        </div>
      </section>

      <section class="actions">
        <button id="runNow" class="action-button run">Run Backup Now</button>
        <button id="restartNow" class="action-button restart">Restart Service</button>
        <button id="stopNow" class="action-button stop">Stop Service</button>
        <button id="authorizeDrive" class="action-button authorize">Authorize Google Drive</button>
        <span id="message" class="message"></span>
      </section>

      <section class="panel">
        <h2 class="panel-title">Restore a backup</h2>
        <p class="restore-warning">Destructive operation: matching collections will be replaced. Type RESTORE to continue.</p>
        <form id="restoreForm" class="restore-form">
          <div class="full"><label for="restoreBackup">Google Drive backup</label><select id="restoreBackup" required><option value="">Load backups first</option></select></div>
          <div><label for="restoreURI">MongoDB URL</label><div class="secret-input"><input id="restoreURI" type="password" autocomplete="off" placeholder="mongodb://..." required><button id="restoreURIToggle" class="secret-toggle" type="button" aria-label="Show MongoDB URL" aria-pressed="false">Show</button></div></div>
          <div><label for="restoreDatabase">Database name</label><input id="restoreDatabase" type="text" autocomplete="off" placeholder="mydatabase" required></div>
          <div><label for="restoreConfirmation">Confirmation</label><input id="restoreConfirmation" type="text" autocomplete="off" placeholder="RESTORE" required></div>
          <div><button id="restoreSubmit" class="action-button stop" type="submit">Restore and replace database</button></div>
        </form>
      </section>

      <section class="dashboard-grid">
        <section class="panel">
          <h2 class="panel-title">Backups in Google Drive</h2>
          <div id="backups" class="backup-list">Loading...</div>
        </section>

        <section class="panel terminal-panel">
          <div class="terminal-head">
            <div class="terminal-title">Log History</div>
            <div class="terminal-buttons">
              <span class="terminal-button red"></span>
              <span class="terminal-button amber"></span>
              <span class="terminal-button green"></span>
            </div>
          </div>
          <div id="logs" class="log-list">Loading...</div>
        </section>
      </section>
    </main>
  </div>

  <script>
    let displayTimezone = localStorage.getItem('displayTimezone') || 'utc';

    function statusClass(status) {
      if (status === 'success') return 'stat-value success';
      if (status === 'failed') return 'stat-value error';
      if (status === 'running') return 'stat-value running';
      return 'stat-value';
    }

    function pad2(n) {
      return String(n).padStart(2, '0');
    }

    function formatISODate(isoString, tz) {
      if (!isoString) return '-';
      const date = new Date(isoString);
      if (isNaN(date.getTime())) return isoString || '-';
      let d, m, y, h, min, s, tzLabel;
      if (tz === 'utc') {
        d = date.getUTCDate();
        m = date.getUTCMonth() + 1;
        y = date.getUTCFullYear();
        h = date.getUTCHours();
        min = date.getUTCMinutes();
        s = date.getUTCSeconds();
        tzLabel = 'UTC';
      } else {
        const utcMs = date.getTime() + (date.getTimezoneOffset() * 60000);
        const local6 = new Date(utcMs + (3600000 * 6));
        d = local6.getUTCDate();
        m = local6.getUTCMonth() + 1;
        y = local6.getUTCFullYear();
        h = local6.getUTCHours();
        min = local6.getUTCMinutes();
        s = local6.getUTCSeconds();
        tzLabel = '+0600';
      }
      return pad2(d) + ' ' + pad2(m) + ' ' + y + ' ' + pad2(h) + ':' + pad2(min) + ':' + pad2(s) + ' ' + tzLabel;
    }

    function updateTimeElement(elementId, isoValue) {
      const el = document.getElementById(elementId);
      if (el) {
        el.textContent = formatISODate(isoValue, displayTimezone);
      }
    }

    function escapeHtml(value) {
      return String(value).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/\"/g, '&quot;').replace(/'/g, '&#039;');
    }

    function readLogLine(line) {
      const ts = line.time || new Date().toISOString();
      const level = String(line.level || 'info').toUpperCase();
      const event = line.event || 'log';
      const fields = line.fields || {};
      const fieldText = Object.keys(fields).length ? ' ' + JSON.stringify(fields, null, 2) : '';
      return '<span class="log-line"><span class="log-level">[' + escapeHtml(ts) + '] ' + escapeHtml(level) + '</span> ' + escapeHtml(event) + escapeHtml(fieldText) + '</span>';
    }

    const tzToggle = document.getElementById('tzToggle');
    if (tzToggle) {
      tzToggle.textContent = displayTimezone === 'utc' ? 'UTC' : 'Local (UTC+6)';
      tzToggle.addEventListener('click', () => {
        displayTimezone = displayTimezone === 'utc' ? 'local' : 'utc';
        localStorage.setItem('displayTimezone', displayTimezone);
        tzToggle.textContent = displayTimezone === 'utc' ? 'UTC' : 'Local (UTC+6)';
      });
    }

    const authorizeButton = document.getElementById('authorizeDrive');
    async function updateAuthorizeButtonState() {
      try {
        const res = await fetch('/api/oauth/status', { method: 'GET' });
        if (res.status !== 200) {
          authorizeButton.disabled = true;
          return;
        }
        const data = await res.json();
        if (!data.configured || data.authorized) {
          authorizeButton.disabled = true;
          authorizeButton.textContent = data.authorized ? 'Google Drive Authorized' : 'Authorize Google Drive';
          return;
        }
        authorizeButton.disabled = false;
        authorizeButton.textContent = 'Authorize Google Drive';
      } catch (err) {
        authorizeButton.disabled = true;
      }
    }
    updateAuthorizeButtonState();

    const protocol = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(protocol + '://' + location.host + '/ws');
    ws.addEventListener('message', (event) => {
      const payload = JSON.parse(event.data);
      if (payload.type === 'status' && payload.status) {
        const data = payload.status;
        document.getElementById('env').textContent = data.environment || '-';
        document.getElementById('database').textContent = data.database || '-';
        document.getElementById('schedule').textContent = data.schedule || '-';
        document.getElementById('timezone').textContent = data.timezone || '-';
        updateTimeElement('current_time', data.current_time);
        updateTimeElement('next_run', data.next_run);
        document.getElementById('countdown').textContent = data.countdown || '-';
        updateTimeElement('last_backup', data.last_backup);
        document.getElementById('last_file').textContent = data.last_file || '-';
        document.getElementById('last_size').textContent = data.last_size || '-';
        document.getElementById('last_error').textContent = data.last_error || '-';
        document.getElementById('progress').textContent = data.progress || '-';
        const statusEl = document.getElementById('last_status');
        statusEl.textContent = data.last_status || '-';
        statusEl.className = statusClass(data.last_status);
      }
      if (payload.type === 'logs' && Array.isArray(payload.logs)) {
        const el = document.getElementById('logs');
        if (!payload.logs.length) {
          el.innerHTML = '<span class="log-line">No logs yet</span>';
          return;
        }
        el.innerHTML = payload.logs.slice(-100).map(readLogLine).join('');
      }
      if (payload.type === 'backups' && Array.isArray(payload.backups)) {
        renderBackups(payload.backups);
      }
    });

    async function fetchBackups() {
      try {
        const res = await fetch('/api/backups');
        if (res.status === 200) {
          const items = await res.json();
          renderBackups(items);
        }
      } catch (err) {
        console.error('failed to fetch backups', err);
      }
    }

    function renderBackups(items) {
      const el = document.getElementById('backups');
      const restoreSelect = document.getElementById('restoreBackup');
      if (restoreSelect) {
        restoreSelect.innerHTML = '<option value="">Select a backup</option>' + items.map(item => '<option value="' + escapeHtml(item.id) + '">' + escapeHtml(item.name) + '</option>').join('');
      }
      if (!items.length) {
        el.textContent = 'No backups found';
        return;
      }
      el.innerHTML = items.map(item => '<div class="backup-item"><a href="https://drive.google.com/open?id=' + encodeURIComponent(item.id) + '" target="_blank" rel="noopener noreferrer">' + escapeHtml(item.name) + '</a><span class="backup-meta">(' + escapeHtml(item.size) + ')</span><span class="backup-meta">' + escapeHtml(formatISODate(item.mtime, displayTimezone)) + '</span></div>').join('');
    }

    document.getElementById('restoreURIToggle').addEventListener('click', () => {
      const input = document.getElementById('restoreURI');
      const toggle = document.getElementById('restoreURIToggle');
      const visible = input.type === 'text';
      input.type = visible ? 'password' : 'text';
      toggle.textContent = visible ? 'Show' : 'Hide';
      toggle.setAttribute('aria-label', visible ? 'Show MongoDB URL' : 'Hide MongoDB URL');
      toggle.setAttribute('aria-pressed', String(!visible));
    });

    document.getElementById('restoreForm').addEventListener('submit', async (event) => {
      event.preventDefault();
      const submit = document.getElementById('restoreSubmit');
      if (!window.confirm('This will replace matching collections in the target database. Continue?')) return;
      submit.disabled = true;
      const msg = document.getElementById('message');
      msg.textContent = 'Restoring backup...';
      try {
        const res = await fetch('/api/restore', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            file_id: document.getElementById('restoreBackup').value,
            mongo_uri: document.getElementById('restoreURI').value,
            database: document.getElementById('restoreDatabase').value,
            confirmation: document.getElementById('restoreConfirmation').value
          })
        });
        msg.textContent = res.ok ? 'Restore completed.' : 'Restore failed: ' + (await res.text());
      } catch (err) {
        msg.textContent = 'Restore failed: ' + err.message;
      } finally {
        submit.disabled = false;
      }
    });

    setTimeout(fetchBackups, 500);

    document.getElementById('runNow').addEventListener('click', async () => {
      const msg = document.getElementById('message');
      msg.textContent = 'Starting...';
      const res = await fetch('/api/backup/now', { method: 'POST' });
      if (res.status === 202) {
        msg.textContent = 'Backup started';
      } else {
        msg.textContent = 'Failed: ' + (await res.text());
      }
    });

    document.getElementById('restartNow').addEventListener('click', async () => {
      const msg = document.getElementById('message');
      msg.textContent = 'Restarting...';
      const res = await fetch('/api/restart', { method: 'POST' });
      if (res.status === 202) {
        msg.textContent = 'Restart signal sent';
      } else {
        msg.textContent = 'Failed: ' + (await res.text());
      }
    });

    document.getElementById('stopNow').addEventListener('click', async () => {
      const msg = document.getElementById('message');
      const button = document.getElementById('stopNow');
      button.disabled = true;
      msg.textContent = 'Stopping service...';
      try {
        const res = await fetch('/api/stop', { method: 'POST' });
        if (res.status === 202) {
          msg.textContent = 'Stop signal sent. The service will shut down shortly.';
        } else {
          msg.textContent = 'Failed: ' + (await res.text());
          button.disabled = false;
        }
      } catch (err) {
        msg.textContent = 'Service stopped or unavailable.';
      }
    });

    document.getElementById('authorizeDrive').addEventListener('click', async () => {
      const msg = document.getElementById('message');
      msg.textContent = 'Starting authorization...';
      try {
        const res = await fetch('/api/oauth/start', { method: 'POST' });
        if (res.status === 200) {
          const data = await res.json();
          msg.textContent = 'Opening authorization page...';
          window.open(data.url, '_blank', 'noopener,noreferrer');
        } else {
          msg.textContent = 'Failed: ' + (await res.text());
        }
      } catch (err) {
        msg.textContent = 'Error: ' + err.message;
      }
    });
  </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html")
	nonce := requestNonce(r)
	html = addCSPNonce(html, nonce)
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy(nonce))
	_, _ = w.Write([]byte(html))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.status)
}

func (s *Server) handleBackupNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	select {
	case s.triggerCh <- struct{}{}:
		s.log.Info("web_manual_backup_triggered", nil)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("backup triggered"))
	default:
		http.Error(w, "backup already running", http.StatusTooManyRequests)
	}
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	select {
	case s.stopCh <- struct{}{}:
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("stop signal sent"))
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("already stopping"))
	}
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	select {
	case s.restartCh <- struct{}{}:
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("restart signal sent"))
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("already restarting"))
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.logsMu.RLock()
	defer s.logsMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.logs)
}

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.logsMu.RLock()
	fn := s.listBackups
	s.logsMu.RUnlock()

	if fn == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	files, err := fn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]BackupItem, 0, len(files))
	for _, f := range files {
		items = append(items, BackupItem{
			ID:    f.Id,
			Name:  f.Name,
			Size:  fmt.Sprintf("%d", f.Size),
			MTime: f.ModifiedTime,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

func formatBytes(b int64) string {
	if b < 1024 {
		return formatInt(b) + " B"
	}
	if b < 1024*1024 {
		return formatInt(b/1024) + " KB"
	}
	if b < 1024*1024*1024 {
		return formatInt(b/(1024*1024)) + " MB"
	}
	return formatInt(b/(1024*1024*1024)) + " GB"
}

func formatInt(n int64) string {
	return formatIntBase(n, 10)
}

func formatIntBase(n int64, base int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [64]byte
	i := len(buf)
	digits := "0123456789"
	for n > 0 {
		i--
		buf[i] = digits[n%int64(base)]
		n /= int64(base)
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
