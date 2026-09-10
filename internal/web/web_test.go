package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/websocket"

	"github.com/sarwar/mongo-drive-backup/internal/logger"
)

func TestHandleIndexShouldNotContainContinuousPollingLoop(t *testing.T) {
	server := NewServer("8080", logger.New("production", io.Discard))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()

	server.handleIndex(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.Code)
	}

	body := resp.Body.String()
	if strings.Contains(body, "setInterval") {
		t.Fatal("index page should not include a continuous polling loop")
	}
}

func TestHandleIndexShouldExposeWebSocketMonitor(t *testing.T) {
	server := NewServer("8080", logger.New("production", io.Discard))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()

	server.handleIndex(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.Code)
	}

	body := resp.Body.String()
	if !strings.Contains(body, "new WebSocket") {
		t.Fatal("index page should include a browser WebSocket client for live monitoring")
	}
	if !strings.Contains(body, "/ws") {
		t.Fatal("index page should point the WebSocket client at the monitoring endpoint")
	}
}

func TestUpdateBackupResultBroadcastsStatusSnapshot(t *testing.T) {
	server := NewServer("8080", logger.New("production", io.Discard))
	mux := http.NewServeMux()
	mux.Handle("/ws", websocket.Handler(server.handleWebSocket))
	base := httptest.NewServer(mux)
	defer base.Close()

	wsURL := strings.Replace(base.URL, "http://", "ws://", 1) + "/ws"
	ws, err := websocket.Dial(wsURL, "", base.URL)
	if err != nil {
		t.Fatalf("dial websocket monitor endpoint: %v", err)
	}
	defer ws.Close()

	for i := 0; i < 3; i++ {
		var payload MonitorPayload
		if err := websocket.JSON.Receive(ws, &payload); err != nil {
			t.Fatalf("read initial websocket payload: %v", err)
		}
	}

	server.UpdateBackupResult("sample.zip", 42, nil)

	var payload MonitorPayload
	if err := websocket.JSON.Receive(ws, &payload); err != nil {
		t.Fatalf("expected a status broadcast after UpdateBackupResult: %v", err)
	}
	if payload.Type != "status" || payload.Status == nil {
		t.Fatalf("expected a websocket status payload, got %#v", payload)
	}
	if payload.Status.LastStatus != "success" || payload.Status.LastFile != "sample.zip" || payload.Status.LastSize != "42 B" {
		t.Fatalf("unexpected status payload: %#v", payload.Status)
	}
}

func TestHandleFaviconShouldServeSVGIcon(t *testing.T) {
	server := NewServer("8080", logger.New("production", io.Discard))
	req := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	resp := httptest.NewRecorder()

	server.handleFavicon(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", resp.Code)
	}
	if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "image/svg+xml") {
		t.Fatalf("expected SVG favicon content type, got %q", got)
	}
	if body := resp.Body.String(); !strings.Contains(body, "<svg") {
		t.Fatal("expected favicon SVG payload")
	}
}
