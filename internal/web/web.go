package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/net/websocket"
	"golang.org/x/oauth2"
	driveapi "google.golang.org/api/drive/v3"

	"github.com/sarwar/mongo-drive-backup/internal/drive"
	"github.com/sarwar/mongo-drive-backup/internal/logger"
)

type Status struct {
	Environment string `json:"environment"`
	MongoURI    string `json:"mongo_uri,omitempty"`
	Database    string `json:"database"`
	Schedule    string `json:"schedule"`
	Timezone    string `json:"timezone"`
	NextRun     string `json:"next_run,omitempty"`
	LastBackup  string `json:"last_backup"`
	LastStatus  string `json:"last_status"`
	LastError   string `json:"last_error,omitempty"`
	LastFile    string `json:"last_file,omitempty"`
	LastSize    string `json:"last_size,omitempty"`
	Progress    string `json:"progress,omitempty"`
}

type BackupItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  string `json:"size"`
	MTime string `json:"mtime"`
}

type MonitorPayload struct {
	Type    string         `json:"type"`
	Status  *Status        `json:"status,omitempty"`
	Logs    []logger.Entry `json:"logs,omitempty"`
	Backups []BackupItem   `json:"backups,omitempty"`
}

type Server struct {
	port         string
	status       Status
	statusMu     sync.RWMutex
	triggerCh    chan struct{}
	log          *logger.Logger
	lastRunMu    sync.RWMutex
	lastRunTime  time.Time
	oauthCodeCh  chan string
	stopCh       chan struct{}
	restartCh    chan struct{}
	logs         []logger.Entry
	logsMu       sync.RWMutex
	listBackups  func() ([]*driveapi.File, error)
	oauthHandler *drive.OAuth2Uploader
	oauthAuthURL string
	oauthMu      sync.RWMutex
	wsMu         sync.RWMutex
	wsConns      map[*websocket.Conn]struct{}
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
		wsConns: make(map[*websocket.Conn]struct{}),
	}
	log.AddHook(s.appendLog)
	return s
}

func (s *Server) appendLog(entry logger.Entry) {
	s.logsMu.Lock()
	s.logs = append(s.logs, entry)
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
	s.logsMu.Unlock()
	s.broadcastMonitor(MonitorPayload{Type: "logs", Logs: s.copyLogs()})
}

func (s *Server) copyLogs() []logger.Entry {
	s.logsMu.RLock()
	defer s.logsMu.RUnlock()
	out := make([]logger.Entry, len(s.logs))
	copy(out, s.logs)
	return out
}

func (s *Server) copyStatus() Status {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.status
}

func (s *Server) broadcastMonitor(payload MonitorPayload) {
	s.wsMu.RLock()
	conns := make([]*websocket.Conn, 0, len(s.wsConns))
	for conn := range s.wsConns {
		conns = append(conns, conn)
	}
	s.wsMu.RUnlock()

	for _, conn := range conns {
		if err := websocket.JSON.Send(conn, payload); err != nil {
			s.wsMu.Lock()
			delete(s.wsConns, conn)
			s.wsMu.Unlock()
			_ = conn.Close()
		}
	}
}

func (s *Server) snapshotBackups() []BackupItem {
	s.logsMu.RLock()
	fn := s.listBackups
	s.logsMu.RUnlock()
	if fn == nil {
		return nil
	}
	files, err := fn()
	if err != nil {
		return nil
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
	return items
}

func (s *Server) SetListBackups(fn func() ([]*driveapi.File, error)) {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	s.listBackups = fn
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

func (s *Server) SetConfig(cfgStatus Status) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status = cfgStatus
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

func (s *Server) UpdateBackupResult(file string, size int64, err error) {
	s.lastRunMu.Lock()
	defer s.lastRunMu.Unlock()
	s.lastRunTime = time.Now()

	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.LastBackup = s.lastRunTime.Format(time.RFC3339)
	s.status.LastFile = file
	s.status.LastSize = formatBytes(size)
	s.status.Progress = ""

	if err != nil {
		s.status.LastStatus = "failed"
		s.status.LastError = err.Error()
	} else {
		s.status.LastStatus = "success"
		s.status.LastError = ""
	}
}

func (s *Server) SetRunning() {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.LastStatus = "running"
	s.status.LastError = ""
	s.status.Progress = ""
}

func (s *Server) SetProgress(progress string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.Progress = progress
}

func (s *Server) handleWebSocket(ws *websocket.Conn) {
	s.wsMu.Lock()
	s.wsConns[ws] = struct{}{}
	s.wsMu.Unlock()
	defer func() {
		s.wsMu.Lock()
		delete(s.wsConns, ws)
		s.wsMu.Unlock()
		_ = ws.Close()
	}()

	status := s.copyStatus()
	_ = websocket.JSON.Send(ws, MonitorPayload{Type: "status", Status: &status})
	_ = websocket.JSON.Send(ws, MonitorPayload{Type: "logs", Logs: s.copyLogs()})
	_ = websocket.JSON.Send(ws, MonitorPayload{Type: "backups", Backups: s.snapshotBackups()})

	for {
		if _, err := ws.Read(make([]byte, 1)); err != nil {
			return
		}
	}
}

func findAssetPath(name string) string {
	wd, err := os.Getwd()
	if err != nil {
		return name
	}

	for {
		candidate := filepath.Join(wd, name)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}
	return name
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	iconPath := findAssetPath("favicon.svg")
	data, err := os.ReadFile(iconPath)
	if err != nil {
		http.Error(w, "favicon not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	iconPath := findAssetPath("logo.svg")
	data, err := os.ReadFile(iconPath)
	if err != nil {
		http.Error(w, "logo not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/favicon.svg", s.handleFavicon)
	mux.HandleFunc("/logo.svg", s.handleLogo)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/backup/now", s.handleBackupNow)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/restart", s.handleRestart)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/backups", s.handleBackups)
	mux.HandleFunc("/api/oauth/start", s.handleOAuthStart)
	mux.HandleFunc("/oauth2callback", s.handleOAuth2Callback)
	mux.Handle("/ws", websocket.Handler(s.handleWebSocket))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	s.log.Info("web_server_started", map[string]interface{}{
		"port": s.port,
	})

	return http.ListenAndServe(":"+s.port, secureHeaders(mux))
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://cdn.tailwindcss.com 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://cdn.tailwindcss.com; img-src 'self' https://drive.google.com data:; connect-src 'self' ws: wss:; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
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
<script src="https://cdn.tailwindcss.com"></script>
<style>
  :root {
    --deep: #01140d;
    --green: #78ff9a;
    --green-soft: #baffcc;
    --green-muted: #2b8f56;
    --green-deep: #073b21;
    --line: #69d793;
    --line-soft: #9affba;
    --panel: #021f15;
    --panel-soft: #032917;
    --text: var(--green-soft);
    --muted: var(--green);
    --subtle: var(--green-muted);
    --primary: var(--green);
    --primary-2: var(--green-soft);
  }

  * {
    box-sizing: border-box;
  }

  body {
    margin: 0;
    font-family: Inter, "Segoe UI", Roboto, Arial, sans-serif;
    background: var(--deep);
    color: var(--text);
    min-height: 100vh;
  }

  .dashboard-wrap {
    max-width: 1440px;
    margin: 0 auto;
    padding: 24px 16px 40px;
  }

  .dashboard-shell {
    display: flex;
    gap: 16px;
    min-height: calc(100vh - 80px);
  }

  .dashboard-grid {
    display: grid;
    grid-template-columns: minmax(280px, 0.95fr) minmax(420px, 1.45fr);
    gap: 16px;
  }

  .sidebar {
    width: 250px;
    background: var(--panel-bg);
    border: 1px solid var(--border);
    border-radius: 16px;
    padding: 16px 14px;
    box-shadow: 0 12px 30px rgba(0,0,0,0.25);
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
    background: var(--green);
    color: var(--deep);
    font-weight: 900;
    font-size: 1.1rem;
    box-shadow: 0 0 22px rgba(120, 255, 154, 0.45);
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
    border-color: var(--line-soft);
    background: var(--panel-soft);
  }

  .nav-link .nav-icon {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--green);
  }

  .sidebar-card {
    border: 1px solid var(--line-soft);
    border-radius: 12px;
    padding: 12px;
    background: var(--panel-soft);
    color: var(--muted);
    font-size: 0.78rem;
    margin-top: 14px;
  }

  .sidebar-card strong {
    color: var(--text);
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
    margin-bottom: 16px;
    background: var(--panel-bg);
    border: 1px solid var(--border);
    border-radius: 16px;
    padding: 16px 18px;
    box-shadow: 0 8px 24px rgba(0,0,0,0.2);
  }

  .dashboard-title {
    margin: 0 0 4px;
    font-size: clamp(1.7rem, 2.5vw, 2.4rem);
    font-weight: 800;
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
    border: 1px solid var(--border);
    background: var(--panel-soft);
    font-size: 0.86rem;
    color: var(--muted);
    white-space: nowrap;
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
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 16px;
    padding: 20px;
    margin-bottom: 16px;
    box-shadow: 0 10px 40px rgba(0,0,0,0.24);
  }

  .stats-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(220px, 1fr));
    gap: 14px;
  }

  .stat-card {
    background: var(--panel-soft);
    border: 1px solid var(--line);
    border-radius: 14px;
    padding: 14px;
    min-height: 88px;
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .stat-label {
    color: var(--muted);
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
    border-radius: 12px;
    font-weight: 800;
    font-size: 0.9rem;
    cursor: pointer;
    transition: transform 180ms ease, filter 180ms ease, opacity 180ms ease;
    background: var(--green);
  }

  .action-button:hover {
    filter: brightness(1.08);
  }

  .action-button:focus-visible {
    outline: 2px solid var(--green-soft);
    outline-offset: 3px;
  }

  .action-button.run {
    background: var(--green);
  }

  .action-button.restart {
    background: var(--green-muted);
    color: var(--green-soft);
  }

  .action-button.authorize {
    background: var(--green-soft);
  }

  .message {
    color: var(--muted);
    font-size: 0.86rem;
  }

  .panel-title {
    margin: 0 0 14px;
    font-size: clamp(1.2rem, 3vw, 1.4rem);
    font-weight: 700;
  }

  .backup-list {
    display: grid;
    gap: 10px;
  }

  .backup-item {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    color: var(--text);
    font-size: 0.86rem;
    word-break: break-all;
    border-bottom: 1px solid var(--line);
    padding-bottom: 8px;
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
  }

  .terminal-panel {
    background: var(--deep);
    border-color: var(--line-soft);
  }

  .terminal-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 0 0 12px;
    border-bottom: 1px solid rgba(129, 140, 248, 0.34);
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
    border: 1px solid var(--line);
    padding: 14px;
    white-space: pre-wrap;
    max-height: 270px;
    overflow: auto;
    line-height: 1.65;
    box-shadow: inset 0 0 15px rgba(120, 255, 154, 0.08);
    background-image: repeating-linear-gradient(180deg, transparent, transparent 4px, rgba(120, 255, 154, 0.03) 4px);
  }

  .log-line {
    display: block;
    border-bottom: 1px dotted var(--line);
    padding: 2px 0;
  }

  .log-line:last-child {
    border-bottom: 0;
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
      </div>
    </aside>

    <main class="main-panel">
      <section class="topbar">
        <div>
          <h1 class="dashboard-title">MongoDB Google Drive Backup</h1>
          <p class="dashboard-subtitle">Service dashboard</p>
        </div>
        <div class="service-chip">online</div>
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
        <button id="authorizeDrive" class="action-button authorize">Authorize Google Drive</button>
        <span id="message" class="message"></span>
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
    function statusClass(status) {
      if (status === 'success') return 'stat-value success';
      if (status === 'failed') return 'stat-value error';
      if (status === 'running') return 'stat-value running';
      return 'stat-value';
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
        document.getElementById('last_backup').textContent = data.last_backup || '-';
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
        const el = document.getElementById('backups');
        if (!payload.backups.length) {
          el.textContent = 'No backups found';
          return;
        }
        el.innerHTML = payload.backups.map(item => '<div class="backup-item"><a href="https://drive.google.com/open?id=' + encodeURIComponent(item.id) + '" target="_blank" rel="noopener noreferrer">' + escapeHtml(item.name) + '</a><span class="backup-meta">(' + escapeHtml(item.size) + ')</span><span class="backup-meta">' + escapeHtml(item.mtime) + '</span></div>').join('');
      }
    });

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

	type BackupItem struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Size  string `json:"size"`
		MTime string `json:"mtime"`
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
