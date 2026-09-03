package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
)

// TestEndToEndHandshake drives the real registry through the real stdio
// transport and the real server, exercising the same path an MCP client uses.
// It needs no TrueNAS: initialize and tools/list never reach the middleware.
func TestEndToEndHandshake(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
	}, "\n") + "\n"

	var out lockedBuffer
	transport := mcp.NewStdioTransportWith(strings.NewReader(input), &out, 0)
	server := mcp.NewServer(NewRegistry(nil, nil), mcp.ServerOptions{
		Name:         "truenas-mcp",
		Version:      "test",
		Instructions: "guidance for the model",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Serve(ctx, transport); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	responses := map[float64]mcp.Response{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var resp mcp.Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("unparseable line %q: %v", line, err)
		}
		var id float64
		if err := json.Unmarshal(resp.ID, &id); err != nil {
			t.Fatalf("unparseable id %q", resp.ID)
		}
		responses[id] = resp
	}

	if len(responses) != 3 {
		t.Fatalf("got %d responses, want 3 (the notification must not be answered)", len(responses))
	}

	var initResult mcp.InitializeResult
	decodeInto(t, responses[1].Result, &initResult)
	if initResult.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q", initResult.ProtocolVersion)
	}
	if initResult.Capabilities.Tools == nil {
		t.Error("server did not advertise the tools capability")
	}
	if initResult.Instructions == "" {
		t.Error("server sent no instructions")
	}

	var list mcp.ToolsListResult
	decodeInto(t, responses[2].Result, &list)
	if len(list.Tools) < 60 {
		t.Fatalf("tools/list returned %d tools, want the full set", len(list.Tools))
	}
	for _, tool := range list.Tools {
		if tool.Annotations == nil {
			t.Errorf("%s crossed the wire without annotations", tool.Name)
		}
	}
}

// TestEndToEndReadOnlyRefusesWrite proves the guard holds through the whole
// stack, not just at the registry boundary.
func TestEndToEndReadOnlyRefusesWrite(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"system_reboot","arguments":{}}}` + "\n"

	var out lockedBuffer
	transport := mcp.NewStdioTransportWith(strings.NewReader(input), &out, 0)
	registry := NewRegistryWithOptions(nil, nil, Options{ReadOnly: true})
	server := mcp.NewServer(registry, mcp.ServerOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Serve(ctx, transport); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var resp mcp.Response
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp); err != nil {
		t.Fatalf("bad response %q: %v", out.String(), err)
	}
	var result mcp.ToolCallResult
	decodeInto(t, resp.Result, &result)

	if !result.IsError {
		t.Fatal("system_reboot succeeded in read-only mode")
	}
	if !strings.Contains(result.Content[0].Text, "not available") {
		t.Fatalf("error text = %q", result.Content[0].Text)
	}
}

type lockedBuffer struct{ bytes.Buffer }

func decodeInto(t *testing.T, from interface{}, into interface{}) {
	t.Helper()
	raw, err := json.Marshal(from)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
