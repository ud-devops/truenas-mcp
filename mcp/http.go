package mcp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPOptions configures the Streamable HTTP transport.
type HTTPOptions struct {
	// Addr is the listen address, e.g. "127.0.0.1:8089".
	Addr string
	// Path is the MCP endpoint path. Defaults to "/mcp".
	Path string
	// BearerToken, when set, is required in an Authorization: Bearer header.
	// It is mandatory for any non-loopback bind: an unauthenticated MCP
	// endpoint is a remote shell onto the NAS.
	BearerToken string
	// AllowedOrigins lists Origin header values accepted from browsers. An
	// empty list rejects all cross-origin requests, which is the safe default
	// against DNS-rebinding attacks on a locally bound server.
	AllowedOrigins []string
	// MaxRequestBytes caps a single request body. Defaults to 16MiB.
	MaxRequestBytes int64
}

const (
	defaultHTTPPath            = "/mcp"
	defaultMaxHTTPRequestBytes = 16 << 20
	sessionHeader              = "Mcp-Session-Id"
	protocolVersionHeader      = "MCP-Protocol-Version"
)

// HTTPHandler exposes a Server over Streamable HTTP so the same binary can
// serve editors that cannot spawn a subprocess (containers, remote IDEs, a
// shared server on the LAN) instead of only stdio.
type HTTPHandler struct {
	server *Server
	opts   HTTPOptions

	sessionsMu sync.Mutex
	sessions   map[string]time.Time
}

// NewHTTPHandler wires an http.Handler in front of server.
func NewHTTPHandler(server *Server, opts HTTPOptions) *HTTPHandler {
	if opts.Path == "" {
		opts.Path = defaultHTTPPath
	}
	if opts.MaxRequestBytes <= 0 {
		opts.MaxRequestBytes = defaultMaxHTTPRequestBytes
	}
	return &HTTPHandler{
		server:   server,
		opts:     opts,
		sessions: make(map[string]time.Time),
	}
}

// IsLoopback reports whether addr binds only to the loopback interface.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		// An empty host in ":8089" means every interface.
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Handler returns the mux serving the MCP endpoint and a health probe.
func (h *HTTPHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(h.opts.Path, h.handleMCP)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}

func (h *HTTPHandler) handleMCP(w http.ResponseWriter, r *http.Request) {
	if !h.checkOrigin(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if !h.checkAuth(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="truenas-mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.handlePost(w, r)
	case http.MethodDelete:
		if id := r.Header.Get(sessionHeader); id != "" {
			h.sessionsMu.Lock()
			delete(h.sessions, id)
			h.sessionsMu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		// This server never initiates messages, so there is no stream to
		// open. Saying so plainly beats leaving the client hanging on an SSE
		// connection that will never carry an event.
		http.Error(w, "server-initiated streams are not supported", http.StatusMethodNotAllowed)
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *HTTPHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body := http.MaxBytesReader(w, r.Body, h.opts.MaxRequestBytes)

	var raw json.RawMessage
	if err := json.NewDecoder(body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, &Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: CodeParseError, Message: "Parse error: " + err.Error()},
		})
		return
	}

	// A batch is a JSON array. We answer each member and return the
	// non-empty responses, matching JSON-RPC batch semantics.
	if isJSONArray(raw) {
		h.handleBatch(w, r, raw)
		return
	}

	isInit := methodOf(raw) == "initialize"
	resp := h.server.HandleMessage(r.Context(), raw)

	if isInit {
		w.Header().Set(sessionHeader, h.newSession())
	}
	if resp == nil {
		// Notification: acknowledged, nothing to say.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *HTTPHandler) handleBatch(w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	var msgs []json.RawMessage
	if err := json.Unmarshal(raw, &msgs); err != nil || len(msgs) == 0 {
		writeJSON(w, http.StatusBadRequest, &Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: CodeInvalidRequest, Message: "Invalid Request: empty or malformed batch"},
		})
		return
	}

	responses := make([]*Response, 0, len(msgs))
	for _, msg := range msgs {
		if resp := h.server.HandleMessage(r.Context(), msg); resp != nil {
			responses = append(responses, resp)
		}
	}
	if len(responses) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, responses)
}

func (h *HTTPHandler) newSession() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// Falling back to a timestamp would make session ids guessable, so
		// prefer no session id at all over a weak one.
		log.Printf("Failed to generate session id: %v", err)
		return ""
	}
	id := hex.EncodeToString(buf)
	h.sessionsMu.Lock()
	h.sessions[id] = time.Now()
	h.sessionsMu.Unlock()
	return id
}

func (h *HTTPHandler) checkAuth(r *http.Request) bool {
	if h.opts.BearerToken == "" {
		return true
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.opts.BearerToken)) == 1
}

// checkOrigin defends a locally bound endpoint against DNS rebinding: a page
// on any website can POST to http://127.0.0.1:8089 from the victim's browser,
// so an Origin we did not allow must be refused.
func (h *HTTPHandler) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser client (no Origin header is ever added by curl or an
		// MCP client library).
		return true
	}
	for _, allowed := range h.opts.AllowedOrigins {
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return true
		}
	}
	if u, err := url.Parse(origin); err == nil && IsLoopback(u.Host) {
		return true
	}
	return false
}

func isJSONArray(raw []byte) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

func methodOf(raw []byte) string {
	var probe struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Method
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("Failed to write HTTP response: %v", err)
	}
}
