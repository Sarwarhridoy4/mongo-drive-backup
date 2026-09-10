package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
