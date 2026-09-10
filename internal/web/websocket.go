package web

import (
	"golang.org/x/net/websocket"

	"github.com/sarwar/mongo-drive-backup/internal/logger"
)

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

func (s *Server) broadcastStatus() {
	status := s.copyStatus()
	s.broadcastMonitor(MonitorPayload{Type: "status", Status: &status})
}

func (s *Server) broadcastBackups() {
	s.broadcastMonitor(MonitorPayload{Type: "backups", Backups: s.snapshotBackups()})
}

func (s *Server) broadcastLogs() {
	s.broadcastMonitor(MonitorPayload{Type: "logs", Logs: s.copyLogs()})
}

func (s *Server) broadcastStatusAndBackups() {
	s.broadcastStatus()
	s.broadcastBackups()
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

func (s *Server) appendLog(entry logger.Entry) {
	s.logsMu.Lock()
	s.logs = append(s.logs, entry)
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
	s.logsMu.Unlock()
	s.broadcastLogs()
}
