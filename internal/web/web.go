package web

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

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
}

type Server struct {
	port        string
	status      Status
	statusMu    sync.RWMutex
	triggerCh   chan struct{}
	log         *logger.Logger
	lastRunMu   sync.RWMutex
	lastRunTime time.Time
}

func NewServer(port string, log *logger.Logger) *Server {
	return &Server{
		port:      port,
		log:       log,
		triggerCh: make(chan struct{}, 1),
		status: Status{
			Environment: "production",
			Schedule:    "0 2 * * *",
			Timezone:    "UTC",
			LastStatus:  "idle",
		},
	}
}

func (s *Server) SetConfig(cfgStatus Status) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status = cfgStatus
}

func (s *Server) Trigger() <-chan struct{} {
	return s.triggerCh
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

	if err != nil {
		s.status.LastStatus = "failed"
		s.status.LastError = err.Error()
	} else {
		s.status.LastStatus = "success"
		s.status.LastError = ""
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/backup/now", s.handleBackupNow)
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
      </div>
    </div>

    <div class="flex items-center gap-3">
      <button id="runNow" class="bg-indigo-600 hover:bg-indigo-500 text-white px-4 py-2 rounded-lg font-medium">
        Run Backup Now
      </button>
      <span id="message" class="text-sm text-slate-400"></span>
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
      const statusEl = document.getElementById('last_status');
      statusEl.textContent = data.last_status;
      statusEl.className = 'mt-1 font-medium ' + (data.last_status === 'success' ? 'text-emerald-400' : data.last_status === 'failed' ? 'text-red-400' : 'text-slate-300');
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

    loadStatus();
    setInterval(loadStatus, 3000);
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
