package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHTTP(opts HTTPOptions) http.Handler {
	srv := NewServer(&fakeRegistry{tools: []Tool{{Name: "system_info"}}}, ServerOptions{Version: "9.9.9"})
	return NewHTTPHandler(srv, opts).Handler()
}

func post(t *testing.T, h http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPInitializeIssuesSession(t *testing.T) {
	h := newTestHTTP(HTTPOptions{})
	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get(sessionHeader) == "" {
		t.Error("initialize did not return a session id")
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad body: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("initialize failed: %+v", resp.Error)
	}
}

func TestHTTPNotificationIsAccepted(t *testing.T) {
	h := newTestHTTP(HTTPOptions{})
	rec := post(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "" {
		t.Fatalf("notification produced a body: %q", rec.Body.String())
	}
}

func TestHTTPBatch(t *testing.T) {
	h := newTestHTTP(HTTPOptions{})
	rec := post(t, h, `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"ping"}]`, nil)

	var resps []Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resps); err != nil {
		t.Fatalf("bad batch body %q: %v", rec.Body.String(), err)
	}
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2 (the notification must not be answered)", len(resps))
	}
}

func TestHTTPRequiresBearerToken(t *testing.T) {
	h := newTestHTTP(HTTPOptions{BearerToken: "s3cret"})

	if rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}
	if rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Authorization": "Bearer wrong"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", rec.Code)
	}
	if rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Authorization": "Bearer s3cret"}); rec.Code != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", rec.Code)
	}
}

// A page on any website can POST to a loopback server from the victim's
// browser, so an unexpected Origin must be refused.
func TestHTTPRejectsForeignOrigin(t *testing.T) {
	h := newTestHTTP(HTTPOptions{})

	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d, want 403", rec.Code)
	}

	rec = post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Origin": "http://127.0.0.1:5173"})
	if rec.Code != http.StatusOK {
		t.Fatalf("loopback origin status = %d, want 200", rec.Code)
	}
}

func TestHTTPAllowedOrigin(t *testing.T) {
	h := newTestHTTP(HTTPOptions{AllowedOrigins: []string{"https://ide.example"}})
	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Origin": "https://ide.example"})
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed origin status = %d, want 200", rec.Code)
	}
}

func TestHTTPGetIsRejected(t *testing.T) {
	h := newTestHTTP(HTTPOptions{})
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func TestHTTPBodyLimit(t *testing.T) {
	h := newTestHTTP(HTTPOptions{MaxRequestBytes: 64})
	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"`+strings.Repeat("z", 512)+`"}}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize status = %d, want 400", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	h := newTestHTTP(HTTPOptions{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8089": true,
		"localhost:8089": true,
		"[::1]:8089":     true,
		"0.0.0.0:8089":   false,
		":8089":          false,
		"192.168.1.5:80": false,
	}
	for addr, want := range cases {
		if got := IsLoopback(addr); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestHandleMessageContextIsHonoured(t *testing.T) {
	srv := NewServer(&fakeRegistry{}, ServerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled context must not stall the request/response transport.
	if resp := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); resp == nil {
		t.Fatal("ping returned no response")
	}
}
