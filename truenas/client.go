package truenas

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/truenas/truenas-mcp/redact"
)

// debugLogging controls whether full request/response bodies are written to
// the log. Off by default: normal operation logs only metadata (method, id,
// result size). Enabled via the TRUENAS_MCP_DEBUG environment variable or the
// --debug flag (see SetDebugLogging). Even when enabled, credential-bearing
// values are redacted before logging.
var debugLogging atomic.Bool

func init() {
	switch strings.ToLower(os.Getenv("TRUENAS_MCP_DEBUG")) {
	case "", "0", "false", "no", "off":
	default:
		debugLogging.Store(true)
	}
}

// SetDebugLogging enables or disables verbose request/response body logging.
func SetDebugLogging(enabled bool) { debugLogging.Store(enabled) }

// DebugLogging reports whether verbose body logging is enabled.
func DebugLogging() bool { return debugLogging.Load() }

type Client struct {
	endpoint  string
	apiKey    string
	tlsConfig *tls.Config

	// connMu protects conn and authenticated; also gates connect/authenticate
	connMu        sync.Mutex
	conn          *websocket.Conn
	authenticated bool

	// writeMu protects concurrent WebSocket writes
	writeMu sync.Mutex

	// pending maps request ID -> response channel for concurrent request multiplexing
	pendingMu sync.Mutex
	pending   map[string]chan *responseResult

	requestID atomic.Uint64

	// timeout bounds how long a single middleware call may take. Stored
	// atomically so it can be tuned without racing in-flight calls.
	timeout atomic.Int64
}

// DefaultRequestTimeout is how long a middleware call may run before the
// client gives up on it.
const DefaultRequestTimeout = 120 * time.Second

// SetRequestTimeout overrides the per-call timeout. A non-positive value
// restores the default. Some operations (a pool scrub summary on a large
// array, an app catalog refresh) legitimately exceed two minutes, so this is
// exposed rather than hard-coded.
func (c *Client) SetRequestTimeout(d time.Duration) {
	if d <= 0 {
		d = DefaultRequestTimeout
	}
	c.timeout.Store(int64(d))
}

func (c *Client) requestTimeout() time.Duration {
	if d := c.timeout.Load(); d > 0 {
		return time.Duration(d)
	}
	return DefaultRequestTimeout
}

type responseResult struct {
	resp *APIResponse
	err  error
}

type ConnectRequest struct {
	Msg     string   `json:"msg"`
	Version string   `json:"version"`
	Support []string `json:"support"`
}

type ConnectResponse struct {
	Msg     string `json:"msg"`
	Session string `json:"session"`
}

type APIRequest struct {
	ID     string        `json:"id"`
	Msg    string        `json:"msg"`
	Method string        `json:"method"`
	Params []interface{} `json:"params,omitempty"`
}

type APIResponse struct {
	ID     string          `json:"id"`
	Msg    string          `json:"msg"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *APIError       `json:"error,omitempty"`
}

type APIError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Trace   interface{} `json:"trace,omitempty"` // Can be string or object
}

func NewClient(endpoint, apiKey string, tlsConfig *tls.Config) (*Client, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint cannot be empty")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("apiKey cannot be empty")
	}
	return &Client{
		endpoint:  endpoint,
		apiKey:    apiKey,
		tlsConfig: tlsConfig,
		pending:   make(map[string]chan *responseResult),
	}, nil
}

// connect establishes the WebSocket connection and starts the read loop.
// Must be called with connMu held.
func (c *Client) connect() error {
	if c.conn != nil {
		return nil
	}

	urls, err := c.buildConnectionURLs()
	if err != nil {
		return err
	}

	wsDialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig:  c.tlsConfig, // Always use TLS config (only wss:// allowed)
		ReadBufferSize:   65536,       // 64KB read buffer to handle large messages
		WriteBufferSize:  65536,       // 64KB write buffer to handle large messages
	}

	var lastErr error
	for _, url := range urls {
		log.Printf("Connecting to %s...", url)
		conn, _, err := wsDialer.Dial(url, nil)
		if err != nil {
			if strings.Contains(err.Error(), "x509:") || strings.Contains(err.Error(), "certificate") {
				err = fmt.Errorf("%w (TrueNAS uses a self-signed certificate by default: pass --tls-ca <cert.pem> to trust it, or --insecure to disable verification at the cost of man-in-the-middle protection)", err)
			}
			log.Printf("Connection failed: %v", err)
			lastErr = err
			continue
		}

		// Set read limit to handle large responses (e.g., large upgrade summaries)
		conn.SetReadLimit(10 * 1024 * 1024) // 10MB

		// Send connect message as per TrueNAS WebSocket protocol
		connectMsg := ConnectRequest{
			Msg:     "connect",
			Version: "1",
			Support: []string{"1"},
		}
		if debugLogging.Load() {
			log.Printf("Sending connect message: %+v", connectMsg)
		}
		if err := conn.WriteJSON(connectMsg); err != nil {
			conn.Close()
			lastErr = fmt.Errorf("failed to send connect message: %w", err)
			continue
		}

		// Read connect response directly (before read loop starts)
		var connectResp ConnectResponse
		if err := conn.ReadJSON(&connectResp); err != nil {
			conn.Close()
			lastErr = fmt.Errorf("failed to read connect response: %w", err)
			continue
		}
		if debugLogging.Load() {
			log.Printf("Received connect response: %+v", connectResp)
		}

		if connectResp.Msg != "connected" {
			conn.Close()
			lastErr = fmt.Errorf("unexpected connect response: %s", connectResp.Msg)
			continue
		}

		c.conn = conn
		c.authenticated = false

		// Start the read loop to multiplex concurrent responses
		go c.readLoop(conn)

		log.Printf("Successfully connected via %s", url)
		return nil
	}

	return fmt.Errorf("all connection attempts failed: %w", lastErr)
}

// readLoop reads all WebSocket responses and routes them to the waiting callers
// via the pending map. Runs as a goroutine for the lifetime of the connection.
func (c *Client) readLoop(conn *websocket.Conn) {
	for {
		var resp APIResponse
		if err := conn.ReadJSON(&resp); err != nil {
			// Connection dropped - fail all pending requests
			c.failAllPending(fmt.Errorf("failed to read response: %w", err))

			// Reset connection state if it's still this connection
			c.connMu.Lock()
			if c.conn == conn {
				c.conn = nil
				c.authenticated = false
			}
			c.connMu.Unlock()
			return
		}

		if debugLogging.Load() {
			// Full bodies only in debug mode, and even then with known
			// credential keys masked and any server echo of the API key
			// scrubbed - responses can carry secrets (e.g. job arguments,
			// app configs) that the server is not guaranteed to redact.
			respJSON, _ := json.Marshal(resp)
			log.Printf("Received response: %s", c.scrubSecrets(string(RedactJSONForLog(respJSON))))
		} else {
			log.Printf("Received response: id=%s msg=%s result=%d bytes", resp.ID, resp.Msg, len(resp.Result))
		}

		// Route response to the waiting caller
		c.pendingMu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.pendingMu.Unlock()

		if ok {
			ch <- &responseResult{resp: &resp}
		} else if resp.ID != "" {
			log.Printf("Warning: received response for unknown request ID %s (may have timed out)", resp.ID)
		}
	}
}

// failAllPending delivers an error to all in-flight requests (called on disconnect)
func (c *Client) failAllPending(err error) {
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan *responseResult)
	c.pendingMu.Unlock()

	for _, ch := range pending {
		ch <- &responseResult{err: err}
	}
}

// DefaultWebSocketPath is the TrueNAS middleware endpoint for the legacy
// DDP-style protocol this client speaks.
const DefaultWebSocketPath = "/websocket"

// buildConnectionURLs returns URLs to try in order.
//
// The endpoint may be a bare hostname ("truenas.local"), a host with a port
// ("truenas.local:8443"), a bracketed or bare IPv6 literal ("[fd00::1]:443",
// "fd00::1"), or a full wss:// / https:// URL.
func (c *Client) buildConnectionURLs() ([]string, error) {
	endpoint := strings.TrimSpace(c.endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("endpoint cannot be empty")
	}

	// SECURITY: Reject unencrypted schemes entirely - TrueNAS revokes API keys
	// used over ws://.
	if strings.HasPrefix(endpoint, "ws://") || strings.HasPrefix(endpoint, "http://") {
		return nil, fmt.Errorf("SECURITY ERROR: unencrypted connections are not allowed. TrueNAS will revoke API keys used over ws://. Use wss:// instead")
	}

	if strings.HasPrefix(endpoint, "wss://") || strings.HasPrefix(endpoint, "https://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("invalid endpoint URL %q: %w", endpoint, err)
		}
		u.Scheme = "wss"
		if u.Path == "" || u.Path == "/" {
			u.Path = DefaultWebSocketPath
		}
		return []string{u.String()}, nil
	}

	host, port, err := splitHostPort(endpoint)
	if err != nil {
		return nil, err
	}

	u := url.URL{Scheme: "wss", Host: net.JoinHostPort(host, port), Path: DefaultWebSocketPath}
	return []string{u.String()}, nil
}

// splitHostPort separates an optional port from a host, defaulting to 443.
//
// A naive strings.LastIndex(":") is wrong twice over: it silently discards a
// port the user deliberately chose (TrueNAS behind a reverse proxy on 8443
// became unreachable), and it truncates a bare IPv6 literal such as "fd00::1"
// to "fd00:".
func splitHostPort(endpoint string) (host, port string, err error) {
	const defaultPort = "443"

	if strings.HasPrefix(endpoint, "[") {
		// Bracketed IPv6, with or without a port.
		if h, p, e := net.SplitHostPort(endpoint); e == nil {
			return h, p, nil
		}
		trimmed := strings.TrimSuffix(strings.TrimPrefix(endpoint, "["), "]")
		if net.ParseIP(trimmed) == nil {
			return "", "", fmt.Errorf("invalid IPv6 endpoint %q", endpoint)
		}
		return trimmed, defaultPort, nil
	}

	// A bare IPv6 literal has more than one colon and no brackets, so it can
	// never be a host:port pair.
	if strings.Count(endpoint, ":") > 1 {
		if net.ParseIP(endpoint) == nil {
			return "", "", fmt.Errorf("invalid endpoint %q: bare IPv6 addresses with a port must be bracketed, e.g. [fd00::1]:443", endpoint)
		}
		return endpoint, defaultPort, nil
	}

	if h, p, e := net.SplitHostPort(endpoint); e == nil {
		if h == "" {
			return "", "", fmt.Errorf("invalid endpoint %q: missing host", endpoint)
		}
		return h, p, nil
	}
	return endpoint, defaultPort, nil
}

func (c *Client) Authenticate() error {
	return c.AuthenticateContext(context.Background())
}

// AuthenticateContext logs in, honouring ctx for cancellation.
func (c *Client) AuthenticateContext(ctx context.Context) error {
	// Ensure connected before authenticating
	c.connMu.Lock()
	err := c.connect()
	c.connMu.Unlock()
	if err != nil {
		return err
	}

	log.Println("Authenticating with TrueNAS middleware...")

	// Call auth.login_with_api_key
	result, err := c.callRaw(ctx, "auth.login_with_api_key", c.apiKey)
	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	var success bool
	if err := json.Unmarshal(result, &success); err != nil {
		return fmt.Errorf("failed to parse authentication response: %w", err)
	}

	if !success {
		return fmt.Errorf("authentication returned false")
	}

	c.connMu.Lock()
	c.authenticated = true
	c.connMu.Unlock()

	log.Println("TrueNAS middleware authentication successful")
	return nil
}

// Call invokes a middleware method with the client's default timeout.
func (c *Client) Call(method string, params ...interface{}) (json.RawMessage, error) {
	return c.CallContext(context.Background(), method, params...)
}

// CallContext invokes a middleware method, aborting when ctx is cancelled.
//
// Cancellation matters here because a tool call the user abandoned would
// otherwise hold its slot for the full request timeout while the middleware
// keeps working on it.
func (c *Client) CallContext(ctx context.Context, method string, params ...interface{}) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Ensure connected and authenticated (serialized to prevent concurrent reconnects)
	c.connMu.Lock()
	if err := c.connect(); err != nil {
		c.connMu.Unlock()
		return nil, err
	}
	needsAuth := !c.authenticated
	c.connMu.Unlock()

	if needsAuth {
		if err := c.AuthenticateContext(ctx); err != nil {
			return nil, fmt.Errorf("re-authentication failed: %w", err)
		}
	}

	return c.callRaw(ctx, method, params...)
}

// redactValue returns a deep copy of v with values under sensitive keys masked.
func redactValue(v interface{}) interface{} { return redact.Value(v) }

// RedactJSONForLog returns raw with values under credential-bearing keys
// (password, bindpw, secret, ...) replaced by "[REDACTED]". Intended for
// sanitizing JSON messages before writing them to logs. Returns raw unchanged
// if it is not valid JSON.
func RedactJSONForLog(raw []byte) []byte { return redact.JSON(raw) }

// scrubSecrets removes the client's API key from server-supplied text (error
// messages, traces, response bodies) in case the server echoes it back.
func (c *Client) scrubSecrets(s string) string {
	if c.apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.apiKey, "[REDACTED]")
}

// redactParams returns a copy of params that is safe for logging. Auth methods
// pass credentials positionally (e.g. auth.login_with_api_key), so every
// parameter is masked; for all other methods, values under sensitive keys are
// masked. The original params are never modified.
func redactParams(method string, params []interface{}) []interface{} {
	if strings.HasPrefix(method, "auth.") {
		redacted := make([]interface{}, len(params))
		for i := range redacted {
			redacted[i] = "[REDACTED]"
		}
		return redacted
	}
	redacted := make([]interface{}, len(params))
	for i, p := range params {
		redacted[i] = redactValue(p)
	}
	return redacted
}

// callRaw sends a request and waits for its response via the pending map.
// Safe for concurrent use.
func (c *Client) callRaw(ctx context.Context, method string, params ...interface{}) (json.RawMessage, error) {
	var lastErr error

	// Try up to 2 times (initial attempt + 1 retry on connection error)
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if attempt > 0 {
			log.Printf("Retrying request after connection error (attempt %d/2)...", attempt+1)
			c.connMu.Lock()
			if err := c.connect(); err != nil {
				c.connMu.Unlock()
				return nil, fmt.Errorf("reconnection failed: %w", err)
			}
			c.connMu.Unlock()
			if err := c.AuthenticateContext(ctx); err != nil {
				return nil, fmt.Errorf("re-authentication failed: %w", err)
			}
		}

		// Snapshot the connection under the lock to avoid nil dereference
		c.connMu.Lock()
		conn := c.conn
		c.connMu.Unlock()

		if conn == nil {
			lastErr = fmt.Errorf("not connected")
			if attempt == 0 {
				// Try to reconnect
				c.connMu.Lock()
				if err := c.connect(); err != nil {
					c.connMu.Unlock()
					return nil, fmt.Errorf("reconnection failed: %w", err)
				}
				c.connMu.Unlock()
				if err := c.AuthenticateContext(ctx); err != nil {
					return nil, fmt.Errorf("re-authentication failed: %w", err)
				}
				continue
			}
			return nil, lastErr
		}

		id := fmt.Sprintf("%d", c.requestID.Add(1))

		// Register the response channel BEFORE writing, to avoid a race where
		// the response arrives before we add the channel to the pending map.
		ch := make(chan *responseResult, 1)
		c.pendingMu.Lock()
		c.pending[id] = ch
		c.pendingMu.Unlock()

		req := APIRequest{
			ID:     id,
			Msg:    "method",
			Method: method,
			Params: params,
		}

		if debugLogging.Load() {
			logReq := req
			logReq.Params = redactParams(method, req.Params)
			reqJSON, _ := json.Marshal(logReq)
			log.Printf("Sending request: %s", string(reqJSON))
		} else {
			log.Printf("Sending request: id=%s method=%s", id, method)
		}

		// writeMu ensures only one goroutine writes to the WebSocket at a time
		c.writeMu.Lock()
		err := conn.WriteJSON(req)
		c.writeMu.Unlock()

		if err != nil {
			// Remove our pending channel since we failed to send
			c.pendingMu.Lock()
			delete(c.pending, id)
			c.pendingMu.Unlock()

			// Clear the connection if it's still this one
			c.connMu.Lock()
			if c.conn == conn {
				c.conn = nil
				c.authenticated = false
			}
			c.connMu.Unlock()

			lastErr = fmt.Errorf("failed to send request: %w", err)
			if isConnectionError(err) && attempt == 0 {
				continue
			}
			return nil, lastErr
		}

		// Wait for the response router to deliver our response
		select {
		case result := <-ch:
			if result.err != nil {
				lastErr = result.err
				if isConnectionError(result.err) && attempt == 0 {
					continue
				}
				return nil, result.err
			}

			resp := result.resp

			if resp.Msg == "failed" {
				if resp.Error != nil {
					return nil, c.formatAPIErrorWithContext(resp.Error, method, params)
				}
				return nil, fmt.Errorf("API call failed with no error details")
			}

			if resp.Error != nil {
				return nil, c.formatAPIErrorWithContext(resp.Error, method, params)
			}

			return resp.Result, nil

		case <-ctx.Done():
			// The caller gave up (client cancellation, shutdown). Drop the
			// pending entry so the read loop does not log an orphan response.
			c.pendingMu.Lock()
			delete(c.pending, id)
			c.pendingMu.Unlock()
			return nil, fmt.Errorf("request cancelled (method: %s): %w", method, ctx.Err())

		case <-time.After(c.requestTimeout()):
			// Timeout - clean up pending entry
			c.pendingMu.Lock()
			delete(c.pending, id)
			c.pendingMu.Unlock()
			return nil, fmt.Errorf("request timed out after %s (method: %s)", c.requestTimeout(), method)
		}
	}

	return nil, lastErr
}

// isConnectionError checks if an error is a connection-related error that should trigger a retry
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "use of closed network connection") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "failed to read response")
}

func (c *Client) Close() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	c.authenticated = false
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// formatAPIErrorWithContext formats API error with request context for
// debugging. Our own params are redacted, and the server-supplied message and
// trace are scrubbed of the API key in case the server echoes it back.
func (c *Client) formatAPIErrorWithContext(apiErr *APIError, method string, params []interface{}) error {
	errMsg := fmt.Sprintf("API error: %s (code %d)", c.scrubSecrets(apiErr.Message), apiErr.Code)

	errMsg = fmt.Sprintf("%s\n\nRequest:\n  Method: %s", errMsg, method)

	if len(params) > 0 {
		if paramsJSON, err := json.MarshalIndent(redactParams(method, params), "  ", "  "); err == nil {
			errMsg = fmt.Sprintf("%s\n  Params: %s", errMsg, string(paramsJSON))
		}
	}

	if apiErr.Trace != nil {
		if traceStr, ok := apiErr.Trace.(string); ok && traceStr != "" {
			errMsg = fmt.Sprintf("%s\n\nTrace: %s", errMsg, c.scrubSecrets(traceStr))
		} else {
			if traceJSON, err := json.MarshalIndent(redactValue(apiErr.Trace), "", "  "); err == nil {
				errMsg = fmt.Sprintf("%s\n\nTrace: %s", errMsg, c.scrubSecrets(string(traceJSON)))
			}
		}
	}

	return fmt.Errorf("%s", errMsg)
}
