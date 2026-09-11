package web

import (
	"context"
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
	CurrentTime string `json:"current_time,omitempty"`
	NextRun     string `json:"next_run,omitempty"`
	Countdown   string `json:"countdown,omitempty"`
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
	s.status.LastBackup = s.lastRunTime.Format("02 Jan 2006 15:04:05 -0700")
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

func (s *Server) SetNextRunGetter(getter func() (time.Time, bool)) {
	s.nextRunGetter = getter
}

func (s *Server) updateTimeStatus() {
	now := time.Now()
	s.statusMu.Lock()
	s.status.CurrentTime = now.Format("02 Jan 2006 15:04:05 -0700")
	if s.nextRunGetter != nil {
		if next, ok := s.nextRunGetter(); ok {
			s.status.NextRun = next.Format("02 Jan 2006 15:04:05 -0700")
			d := next.Sub(now)
			if d < 0 {
				d = 0
			}
			s.status.Countdown = formatDuration(d)
		} else {
			s.status.NextRun = ""
			s.status.Countdown = ""
		}
	}
	s.statusMu.Unlock()
	s.broadcastStatus()
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, sec)
}

func (s *Server) StartTicker(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.updateTimeStatus()
		case <-ctx.Done():
			return
		}
	}
}
