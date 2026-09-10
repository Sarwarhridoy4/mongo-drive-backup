package web

import (
	"fmt"
	"time"

	"google.golang.org/api/drive/v3"

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

func (s *Server) SetListBackups(fn func() ([]*drive.File, error)) {
	s.logsMu.Lock()
	s.listBackups = fn
	s.logsMu.Unlock()
	s.broadcastBackups()
}

func (s *Server) SetConfig(cfgStatus Status) {
	s.statusMu.Lock()
	s.status = cfgStatus
	s.statusMu.Unlock()
	s.broadcastStatus()
}

func (s *Server) UpdateBackupResult(file string, size int64, err error) {
	s.lastRunMu.Lock()
	s.lastRunTime = time.Now()
	s.lastRunMu.Unlock()

	s.statusMu.Lock()
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
	s.statusMu.Unlock()

	s.broadcastStatusAndBackups()
}

func (s *Server) SetRunning() {
	s.statusMu.Lock()
	s.status.LastStatus = "running"
	s.status.LastError = ""
	s.status.Progress = ""
	s.statusMu.Unlock()

	s.broadcastStatus()
}

func (s *Server) SetProgress(progress string) {
	s.statusMu.Lock()
	s.status.Progress = progress
	s.statusMu.Unlock()

	s.broadcastStatus()
}
