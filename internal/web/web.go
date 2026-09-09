package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

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

type Server struct {
	port        string
	status      Status
	statusMu    sync.RWMutex
	triggerCh   chan struct{}
	log         *logger.Logger
	lastRunMu   sync.RWMutex
	lastRunTime time.Time
	oauthCodeCh chan string
	stopCh      chan struct{}
	logs        []logger.Entry
	logsMu      sync.RWMutex
	listBackups func() ([]*driveapi.File, error)
	oauthHandler *drive.OAuth2Uploader
	oauthAuthURL string
	oauthMu     sync.RWMutex
}

func NewServer(port string, log *logger.Logger) *Server {
	s := &Server{
		port:        port,
		log:         log,
		triggerCh:   make(chan struct{}, 1),
		oauthCodeCh: make(chan string, 1),
		stopCh:      make(chan struct{}),
		logs:        make([]logger.Entry, 0, 200),
		status: Status{
			Environment: "production",
			Schedule:    "0 2 * * *",
			Timezone:    "UTC",
			LastStatus:  "idle",
		},
	}
	log.AddHook(s.appendLog)
	return s
}

func (s *Server) appendLog(entry logger.Entry) {
	s.logsMu.Lock()
	defer s.logsMu.Unlock()
	s.logs = append(s.logs, entry)
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
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

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/backup/now", s.handleBackupNow)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/backups", s.handleBackups)
	mux.HandleFunc("/api/oauth/start", s.handleOAuthStart)
	mux.HandleFunc("/oauth2callback", s.handleOAuth2Callback)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	s.log.Info("web_server_started", map[string]interface{}{
		"port": s.port,
	})

	return http.ListenAndServe(":"+s.port, mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>MongoDB Backup Service</title>
<script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-slate-950 text-slate-100 min-h-screen">
  <div class="max-w-3xl mx-auto px-4 py-10">
    <h1 class="text-2xl font-semibold mb-6">MongoDB Google Drive Backup</h1>
    <div class="bg-slate-900 border border-slate-800 rounded-xl p-6 mb-6">
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <div class="text-xs text-slate-400">Environment</div>
          <div id="env" class="mt-1 font-medium">-</div>
        </div>
        <div>
          <div class="text-xs text-slate-400">Database</div>
          <div id="database" class="mt-1 font-medium">-</div>
        </div>
        <div>
          <div class="text-xs text-slate-400">Schedule</div>
          <div id="schedule" class="mt-1 font-medium">-</div>
        </div>
        <div>
          <div class="text-xs text-slate-400">Timezone</div>
          <div id="timezone" class="mt-1 font-medium">-</div>
        </div>
        <div>
          <div class="text-xs text-slate-400">Last Backup</div>
          <div id="last_backup" class="mt-1 font-medium">-</div>
        </div>
        <div>
          <div class="text-xs text-slate-400">Status</div>
          <div id="last_status" class="mt-1 font-medium">-</div>
        </div>
        <div class="sm:col-span-2">
          <div class="text-xs text-slate-400">Last File</div>
          <div id="last_file" class="mt-1 font-medium break-all">-</div>
        </div>
        <div>
          <div class="text-xs text-slate-400">Last Size</div>
          <div id="last_size" class="mt-1 font-medium">-</div>
        </div>
        <div>
          <div class="text-xs text-slate-400">Last Error</div>
          <div id="last_error" class="mt-1 font-medium text-red-400 break-all">-</div>
        </div>
        <div class="sm:col-span-2">
          <div class="text-xs text-slate-400">Progress</div>
          <div id="progress" class="mt-1 font-medium text-slate-300">-</div>
        </div>
      </div>
    </div>

    <div class="flex items-center gap-3">
      <button id="runNow" class="bg-indigo-600 hover:bg-indigo-500 text-white px-4 py-2 rounded-lg font-medium">
        Run Backup Now
      </button>
      <button id="stopNow" class="bg-red-600 hover:bg-red-500 text-white px-4 py-2 rounded-lg font-medium">
        Stop Service
      </button>
      <button id="authorizeDrive" class="bg-emerald-600 hover:bg-emerald-500 text-white px-4 py-2 rounded-lg font-medium">
        Authorize Google Drive
      </button>
      <span id="message" class="text-sm text-slate-400"></span>
    </div>

    <div class="bg-slate-900 border border-slate-800 rounded-xl p-6 mb-6">
      <h2 class="text-lg font-semibold mb-4">Backups in Google Drive</h2>
      <div id="backups" class="text-sm text-slate-300">Loading...</div>
    </div>

    <div class="bg-slate-900 border border-slate-800 rounded-xl p-6 mb-6">
      <h2 class="text-lg font-semibold mb-4">Log History</h2>
      <div id="logs" class="text-sm text-slate-300 font-mono whitespace-pre-wrap max-h-96 overflow-auto">Loading...</div>
    </div>
  </div>

  <script>
    async function loadStatus() {
      const res = await fetch('/api/status');
      const data = await res.json();
      document.getElementById('env').textContent = data.environment;
      document.getElementById('database').textContent = data.database;
      document.getElementById('schedule').textContent = data.schedule;
      document.getElementById('timezone').textContent = data.timezone;
      document.getElementById('last_backup').textContent = data.last_backup || '-';
      document.getElementById('last_file').textContent = data.last_file || '-';
      document.getElementById('last_size').textContent = data.last_size || '-';
      document.getElementById('last_error').textContent = data.last_error || '-';
      document.getElementById('progress').textContent = data.progress || '-';
      const statusEl = document.getElementById('last_status');
      statusEl.textContent = data.last_status;
      statusEl.className = 'mt-1 font-medium ' + (data.last_status === 'success' ? 'text-emerald-400' : data.last_status === 'failed' ? 'text-red-400' : data.last_status === 'running' ? 'text-amber-400' : 'text-slate-300');
    }

    async function loadLogs() {
      const res = await fetch('/api/logs');
      const logs = await res.json();
      const el = document.getElementById('logs');
      if (!logs.length) {
        el.textContent = 'No logs yet';
        return;
      }
      el.textContent = logs.slice(-100).map(l => '[' + l.time + '] ' + l.level.toUpperCase() + ' ' + l.event + ' ' + JSON.stringify(l.fields || {})).join('\n');
    }

    async function loadBackups() {
      const res = await fetch('/api/backups');
      if (res.status === 204) {
        document.getElementById('backups').textContent = 'No backups found';
        return;
      }
      const items = await res.json();
      const el = document.getElementById('backups');
      if (!items.length) {
        el.textContent = 'No backups found';
        return;
      }
      el.innerHTML = items.map(item => '<div class="mb-2 break-all"><a href="https://drive.google.com/open?id=' + item.id + '" target="_blank" class="text-indigo-400 hover:underline">' + item.name + '</a> <span class="text-slate-400">(' + item.size + ')</span> <span class="text-slate-500">' + item.mtime + '</span></div>').join('');
    }

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

    document.getElementById('stopNow').addEventListener('click', async () => {
      const msg = document.getElementById('message');
      msg.textContent = 'Stopping...';
      const res = await fetch('/api/stop', { method: 'POST' });
      if (res.status === 202) {
        msg.textContent = 'Stop signal sent';
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
          window.open(data.url, '_blank');
        } else {
          msg.textContent = 'Failed: ' + (await res.text());
        }
      } catch (err) {
        msg.textContent = 'Error: ' + err.message;
      }
    });

    loadStatus();
    loadLogs();
    loadBackups();
    setInterval(() => {
      loadStatus();
      loadLogs();
      loadBackups();
    }, 3000);
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
	}(r.Context(), config)

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
