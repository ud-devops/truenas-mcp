package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
)

// SupportedProtocolVersions lists the MCP revisions this server speaks, newest
// first. During initialize the client's requested version is echoed back when
// we support it; otherwise we answer with the newest one we do support and let
// the client decide whether to continue.
var SupportedProtocolVersions = []string{
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

// LatestProtocolVersion is the version advertised when the client asks for
// something we do not recognise.
const LatestProtocolVersion = "2025-06-18"

// DefaultMaxConcurrentCalls bounds how many tool calls run at once. Tool calls
// reach TrueNAS over a single multiplexed WebSocket, so some concurrency is a
// large win (a slow scrub query no longer blocks an unrelated system_info),
// but unbounded fan-out would let one client queue thousands of middleware
// requests.
const DefaultMaxConcurrentCalls = 8

// Transport is a bidirectional stream of JSON-RPC messages. Implementations
// must be safe for one reader and any number of concurrent writers.
type Transport interface {
	// Read returns the next raw JSON-RPC message. It returns io.EOF when the
	// peer has gone away.
	Read() ([]byte, error)
	// Write sends one raw JSON-RPC message.
	Write([]byte) error
	// Close releases the transport.
	Close() error
}

// ServerOptions configures a Server.
type ServerOptions struct {
	Name    string
	Version string
	// Instructions is optional guidance shown to the model on connect.
	Instructions string
	// MaxConcurrentCalls bounds in-flight tools/call handlers. Zero selects
	// DefaultMaxConcurrentCalls; a negative value disables the bound.
	MaxConcurrentCalls int
	Debug              bool
}

// Server implements the MCP methods this project needs on top of any
// Transport. It is deliberately transport-agnostic so that stdio and HTTP
// share one dispatch path and one set of semantics.
type Server struct {
	registry ToolRegistry
	opts     ServerOptions

	sem chan struct{}

	// inflight tracks cancellable requests by their JSON-RPC id so that
	// notifications/cancelled can stop work already in progress.
	inflightMu sync.Mutex
	inflight   map[string]context.CancelFunc

	initialized bool
	initMu      sync.RWMutex
}

// NewServer creates a Server serving tools from registry.
func NewServer(registry ToolRegistry, opts ServerOptions) *Server {
	if opts.Name == "" {
		opts.Name = "truenas-mcp"
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	limit := opts.MaxConcurrentCalls
	if limit == 0 {
		limit = DefaultMaxConcurrentCalls
	}
	s := &Server{
		registry: registry,
		opts:     opts,
		inflight: make(map[string]context.CancelFunc),
	}
	if limit > 0 {
		s.sem = make(chan struct{}, limit)
	}
	return s
}

// Serve reads messages from t until the peer disconnects or ctx is cancelled.
//
// Requests are dispatched concurrently: the read loop never blocks on a slow
// tool call, so a long-running upgrade_app no longer stalls every other
// request on the connection.
func (s *Server) Serve(ctx context.Context, t Transport) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	defer wg.Wait()

	// Unblock the blocking Read below when the caller cancels.
	go func() {
		<-ctx.Done()
		_ = t.Close()
	}()

	for {
		raw, err := t.Read()
		if err != nil {
			// The client closing the stream is how an MCP session ends
			// normally, so EOF is a clean shutdown rather than a failure.
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}

		if s.opts.Debug {
			log.Printf("[MCP <-] %s", RedactForLog(raw))
		}

		req, rpcErr := parseRequest(raw)
		if rpcErr != nil {
			s.write(t, &Response{JSONRPC: "2.0", Error: rpcErr})
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			s.dispatch(ctx, t, req)
		}()
	}
}

// parseRequest validates envelope-level requirements before dispatch.
func parseRequest(raw []byte) (*Request, *Error) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &Error{Code: CodeParseError, Message: fmt.Sprintf("Parse error: %v", err)}
	}
	if req.Method == "" {
		return nil, &Error{Code: CodeInvalidRequest, Message: "Invalid Request: missing method"}
	}
	return &req, nil
}

func (s *Server) dispatch(ctx context.Context, t Transport, req *Request) {
	// Notifications are fire-and-forget and must never produce a response.
	if req.IsNotification() {
		s.handleNotification(req)
		return
	}

	reqCtx, cancel := context.WithCancel(ctx)
	key := string(req.ID)
	s.inflightMu.Lock()
	s.inflight[key] = cancel
	s.inflightMu.Unlock()
	defer func() {
		s.inflightMu.Lock()
		delete(s.inflight, key)
		s.inflightMu.Unlock()
		cancel()
	}()

	resp := s.handleRequest(reqCtx, req)
	if resp == nil {
		return
	}
	// A cancelled request gets no reply: the client has already stopped
	// waiting for one and the spec forbids answering a cancelled id.
	if reqCtx.Err() != nil {
		return
	}
	s.write(t, resp)
}

func (s *Server) handleNotification(req *Request) {
	switch req.Method {
	case "notifications/initialized":
		s.initMu.Lock()
		s.initialized = true
		s.initMu.Unlock()
	case "notifications/cancelled":
		var p CancelledParams
		if err := json.Unmarshal(req.Params, &p); err != nil || len(p.RequestID) == 0 {
			return
		}
		s.inflightMu.Lock()
		cancel, ok := s.inflight[string(p.RequestID)]
		s.inflightMu.Unlock()
		if ok {
			if s.opts.Debug {
				log.Printf("Cancelling request %s: %s", p.RequestID, p.Reason)
			}
			cancel()
		}
	}
}

func (s *Server) handleRequest(ctx context.Context, req *Request) *Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		// Keepalive: an empty result is the whole contract.
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return errorResponse(req.ID, CodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func (s *Server) handleInitialize(req *Request) *Response {
	var params InitializeParams
	if len(req.Params) > 0 {
		// A malformed initialize is not fatal; fall back to the latest
		// version rather than refusing the connection outright.
		_ = json.Unmarshal(req.Params, &params)
	}

	version := LatestProtocolVersion
	for _, v := range SupportedProtocolVersions {
		if v == params.ProtocolVersion {
			version = v
			break
		}
	}
	if params.ProtocolVersion != "" && version != params.ProtocolVersion {
		log.Printf("Client requested unsupported protocol version %q; responding with %s",
			params.ProtocolVersion, version)
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: InitializeResult{
			ProtocolVersion: version,
			ServerInfo:      ServerInfo{Name: s.opts.Name, Version: s.opts.Version},
			Capabilities:    Capabilities{Tools: &ToolsCapability{ListChanged: false}},
			Instructions:    s.opts.Instructions,
		},
	}
}

func (s *Server) handleToolsList(req *Request) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolsListResult{Tools: s.registry.ListTools()},
	}
}

func (s *Server) handleToolsCall(ctx context.Context, req *Request) *Response {
	var params ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResponse(req.ID, CodeInvalidParams, fmt.Sprintf("Invalid params: %v", err))
	}
	if params.Name == "" {
		return errorResponse(req.ID, CodeInvalidParams, "Invalid params: tool name is required")
	}

	if s.sem != nil {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		case <-ctx.Done():
			return nil
		}
	}

	result, err := s.registry.CallTool(ctx, params.Name, params.Arguments)
	if err != nil {
		// Tool failures are reported in-band via isError so the model can see
		// and react to them, rather than as protocol-level errors.
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
				IsError: true,
			},
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: ToolCallResult{
			Content: []ContentBlock{{Type: "text", Text: result}},
		},
	}
}

func errorResponse(id json.RawMessage, code int, message string) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Error: &Error{Code: code, Message: message}}
}

func (s *Server) write(t Transport, resp *Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Failed to marshal response: %v", err)
		return
	}
	if s.opts.Debug {
		log.Printf("[MCP ->] %s", RedactForLog(data))
	}
	if err := t.Write(data); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

// HandleMessage dispatches a single request and returns its response, or nil
// for notifications. It exists for request/response transports (HTTP) that do
// not own a long-lived stream.
func (s *Server) HandleMessage(ctx context.Context, raw []byte) *Response {
	req, rpcErr := parseRequest(raw)
	if rpcErr != nil {
		return &Response{JSONRPC: "2.0", Error: rpcErr}
	}
	if req.IsNotification() {
		s.handleNotification(req)
		return nil
	}
	return s.handleRequest(ctx, req)
}
