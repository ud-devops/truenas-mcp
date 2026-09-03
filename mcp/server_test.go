package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRegistry serves canned tools without touching TrueNAS.
type fakeRegistry struct {
	tools []Tool
	call  func(ctx context.Context, name string, args map[string]interface{}) (string, error)
}

func (f *fakeRegistry) ListTools() []Tool { return f.tools }

func (f *fakeRegistry) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if f.call != nil {
		return f.call(ctx, name, args)
	}
	return "ok", nil
}

// pipeTransport drives a Server from a test goroutine.
type pipeTransport struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}
	once   sync.Once
}

func newPipeTransport() *pipeTransport {
	return &pipeTransport{
		in:     make(chan []byte, 16),
		out:    make(chan []byte, 16),
		closed: make(chan struct{}),
	}
}

func (p *pipeTransport) Read() ([]byte, error) {
	select {
	case msg := <-p.in:
		return msg, nil
	case <-p.closed:
		return nil, io.EOF
	}
}

func (p *pipeTransport) Write(b []byte) error {
	cp := append([]byte(nil), b...)
	select {
	case p.out <- cp:
		return nil
	case <-p.closed:
		return io.ErrClosedPipe
	}
}

func (p *pipeTransport) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func (p *pipeTransport) send(t *testing.T, msg string) {
	t.Helper()
	select {
	case p.in <- []byte(msg):
	case <-time.After(2 * time.Second):
		t.Fatal("timed out sending to server")
	}
}

func (p *pipeTransport) recv(t *testing.T) Response {
	t.Helper()
	select {
	case raw := <-p.out:
		var resp Response
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("bad response %q: %v", raw, err)
		}
		return resp
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for response")
		return Response{}
	}
}

func startServer(t *testing.T, reg ToolRegistry) (*pipeTransport, func()) {
	t.Helper()
	srv := NewServer(reg, ServerOptions{Name: "test", Version: "1.2.3"})
	tp := newPipeTransport()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, tp)
	}()
	return tp, func() {
		cancel()
		<-done
	}
}

// TestStdioTransportHandlesLargeMessages is the regression test for the
// defect that made the server die on big payloads: a bufio.Scanner caps a
// token at 64KB, so a single oversized tools/call terminated the read loop
// with "token too long" and the client saw the process go silent.
func TestStdioTransportHandlesLargeMessages(t *testing.T) {
	huge := strings.Repeat("x", 512*1024)
	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"blob":%q}}}`, huge)

	var out strings.Builder
	tp := NewStdioTransportWith(strings.NewReader(msg+"\n"), &out, 0)

	raw, err := tp.Read()
	if err != nil {
		t.Fatalf("Read of a %d byte message failed: %v", len(msg), err)
	}
	if len(raw) < 512*1024 {
		t.Fatalf("message truncated: got %d bytes, want >= %d", len(raw), 512*1024)
	}

	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("large message did not survive intact: %v", err)
	}
	if req.Method != "tools/call" {
		t.Fatalf("method = %q, want tools/call", req.Method)
	}
}

func TestStdioTransportRejectsOversizeMessage(t *testing.T) {
	msg := fmt.Sprintf(`{"blob":%q}`, strings.Repeat("y", 2048))
	tp := NewStdioTransportWith(strings.NewReader(msg+"\n"), &strings.Builder{}, 1024)
	if _, err := tp.Read(); err == nil {
		t.Fatal("expected oversize message to be rejected")
	}
}

func TestInitializeNegotiatesProtocolVersion(t *testing.T) {
	tests := []struct {
		requested string
		want      string
	}{
		{"2024-11-05", "2024-11-05"},
		{"2025-06-18", "2025-06-18"},
		{"1999-01-01", LatestProtocolVersion},
		{"", LatestProtocolVersion},
	}

	for _, tc := range tests {
		t.Run(tc.requested, func(t *testing.T) {
			tp, stop := startServer(t, &fakeRegistry{})
			defer stop()

			tp.send(t, fmt.Sprintf(
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"clientInfo":{"name":"c","version":"0"}}}`,
				tc.requested))

			resp := tp.recv(t)
			var result InitializeResult
			remarshal(t, resp.Result, &result)

			if result.ProtocolVersion != tc.want {
				t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, tc.want)
			}
			if result.ServerInfo.Version != "1.2.3" {
				t.Errorf("serverInfo.version = %q, want 1.2.3", result.ServerInfo.Version)
			}
		})
	}
}

func TestPing(t *testing.T) {
	tp, stop := startServer(t, &fakeRegistry{})
	defer stop()

	tp.send(t, `{"jsonrpc":"2.0","id":7,"method":"ping"}`)
	resp := tp.recv(t)
	if resp.Error != nil {
		t.Fatalf("ping returned error: %+v", resp.Error)
	}
	if string(resp.ID) != "7" {
		t.Errorf("id = %s, want 7", resp.ID)
	}
}

func TestNotificationsProduceNoResponse(t *testing.T) {
	tp, stop := startServer(t, &fakeRegistry{})
	defer stop()

	tp.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	tp.send(t, `{"jsonrpc":"2.0","id":9,"method":"ping"}`)

	// The ping reply must be the first thing on the wire: if the
	// notification had been answered, this would see that instead.
	resp := tp.recv(t)
	if string(resp.ID) != "9" {
		t.Fatalf("first response id = %s, want 9 (a notification was answered)", resp.ID)
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	tp, stop := startServer(t, &fakeRegistry{})
	defer stop()

	tp.send(t, `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	resp := tp.recv(t)
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("error = %+v, want code %d", resp.Error, CodeMethodNotFound)
	}
}

// TestConcurrentDispatch pins the behaviour that a slow tool call no longer
// blocks the whole connection. Under the previous sequential loop the second
// request could not be answered until the first returned.
func TestConcurrentDispatch(t *testing.T) {
	release := make(chan struct{})
	reg := &fakeRegistry{
		call: func(ctx context.Context, name string, _ map[string]interface{}) (string, error) {
			if name == "slow" {
				<-release
			}
			return name, nil
		},
	}

	tp, stop := startServer(t, reg)
	defer stop()

	tp.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow"}}`)
	tp.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fast"}}`)

	resp := tp.recv(t)
	if string(resp.ID) != "2" {
		t.Fatalf("first response id = %s, want 2: a slow call is still blocking the read loop", resp.ID)
	}

	close(release)
	if resp := tp.recv(t); string(resp.ID) != "1" {
		t.Fatalf("second response id = %s, want 1", resp.ID)
	}
}

func TestCancellationStopsToolCall(t *testing.T) {
	started := make(chan struct{})
	observed := make(chan error, 1)

	reg := &fakeRegistry{
		call: func(ctx context.Context, name string, _ map[string]interface{}) (string, error) {
			close(started)
			<-ctx.Done()
			observed <- ctx.Err()
			return "", ctx.Err()
		},
	}

	tp, stop := startServer(t, reg)
	defer stop()

	tp.send(t, `{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"hang"}}`)
	<-started
	tp.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":42,"reason":"user"}}`)

	select {
	case err := <-observed:
		if err == nil {
			t.Fatal("handler context was not cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not reach the tool handler")
	}

	// A cancelled request must not be answered.
	select {
	case raw := <-tp.out:
		t.Fatalf("cancelled request received a response: %s", raw)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestToolErrorIsReportedInBand(t *testing.T) {
	reg := &fakeRegistry{
		call: func(context.Context, string, map[string]interface{}) (string, error) {
			return "", fmt.Errorf("pool is offline")
		},
	}
	tp, stop := startServer(t, reg)
	defer stop()

	tp.send(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"x"}}`)
	resp := tp.recv(t)

	if resp.Error != nil {
		t.Fatalf("tool failure became a protocol error: %+v", resp.Error)
	}
	var result ToolCallResult
	remarshal(t, resp.Result, &result)
	if !result.IsError {
		t.Fatal("isError not set on a failed tool call")
	}
	if !strings.Contains(result.Content[0].Text, "pool is offline") {
		t.Fatalf("error text = %q, want it to mention the cause", result.Content[0].Text)
	}
}

func TestMalformedJSONReturnsParseError(t *testing.T) {
	srv := NewServer(&fakeRegistry{}, ServerOptions{})
	resp := srv.HandleMessage(context.Background(), []byte(`{"jsonrpc":`))
	if resp == nil || resp.Error == nil || resp.Error.Code != CodeParseError {
		t.Fatalf("resp = %+v, want parse error", resp)
	}
}

func TestMissingMethodIsInvalidRequest(t *testing.T) {
	srv := NewServer(&fakeRegistry{}, ServerOptions{})
	resp := srv.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1}`))
	if resp == nil || resp.Error == nil || resp.Error.Code != CodeInvalidRequest {
		t.Fatalf("resp = %+v, want invalid request", resp)
	}
}

func TestToolsListPassesThroughAnnotations(t *testing.T) {
	reg := &fakeRegistry{tools: []Tool{{
		Name:        "query_pools",
		Description: "d",
		InputSchema: map[string]interface{}{"type": "object"},
		Annotations: &ToolAnnotations{ReadOnlyHint: Ptr(true)},
	}}}
	tp, stop := startServer(t, reg)
	defer stop()

	tp.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var result ToolsListResult
	remarshal(t, tp.recv(t).Result, &result)

	if len(result.Tools) != 1 || !result.Tools[0].IsReadOnly() {
		t.Fatalf("annotations lost in tools/list: %+v", result.Tools)
	}
}

func remarshal(t *testing.T, from interface{}, into interface{}) {
	t.Helper()
	raw, err := json.Marshal(from)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
