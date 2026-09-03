package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/tasks"
	"github.com/truenas/truenas-mcp/tools"
	"github.com/truenas/truenas-mcp/truenas"
)

var (
	truenasURL = flag.String("truenas-url", "", "TrueNAS hostname or WebSocket URL (e.g., 'truenas.local', 'truenas.local:8443' or 'wss://10.0.0.1/websocket')")
	apiKey     = flag.String("api-key", "", "TrueNAS API key for middleware authentication")
	insecure   = flag.Bool("insecure", false, "Disable TLS certificate verification (UNSAFE: allows man-in-the-middle attacks)")
	tlsCA      = flag.String("tls-ca", "", "Path to a PEM certificate to trust (e.g., the TrueNAS self-signed certificate)")
	versionFlg = flag.Bool("version", false, "Print version and exit")
	debug      = flag.Bool("debug", false, "Enable debug logging")

	readOnly   = flag.Bool("read-only", false, "Expose only tools that cannot change the system")
	allowTools = flag.String("allow-tools", "", "Comma-separated allowlist of tool names (default: all)")
	denyTools  = flag.String("deny-tools", "", "Comma-separated denylist of tool names")
	listTools  = flag.Bool("list-tools", false, "Print the tools this configuration exposes and exit")

	requestTimeout = flag.Duration("request-timeout", truenas.DefaultRequestTimeout, "Timeout for a single TrueNAS middleware call")
	maxConcurrent  = flag.Int("max-concurrent", mcp.DefaultMaxConcurrentCalls, "Maximum tool calls executed at once (-1 for unlimited)")

	httpAddr    = flag.String("http-addr", "", "Serve MCP over Streamable HTTP on this address instead of stdio (e.g., 127.0.0.1:8089)")
	httpPath    = flag.String("http-path", "/mcp", "HTTP path for the MCP endpoint")
	httpToken   = flag.String("http-token", "", "Bearer token required by the HTTP transport (env: TRUENAS_MCP_HTTP_TOKEN). Required for non-loopback binds")
	httpOrigins = flag.String("http-allowed-origins", "", "Comma-separated browser Origins allowed to call the HTTP endpoint")
)

// Version is the release version, injected at build time via
// -ldflags "-X main.Version=...". Builds without injection report "dev".
var Version = "dev"

const serverInstructions = `This server manages a TrueNAS system over its middleware API.
Prefer the query_* and get_* tools to understand the system before changing it.
Tools that change state are annotated; those annotated destructive can interrupt
service or lose data, so confirm with the user before calling them.`

func main() {
	flag.Parse()

	if *versionFlg {
		fmt.Printf("truenas-mcp version %s\n", Version)
		return
	}

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	applyEnvDefaults()

	if *listTools {
		// Listing the exposed surface must not require a reachable NAS: it is
		// how someone checks what --read-only or --deny-tools actually does.
		registry := tools.NewRegistryWithOptions(nil, nil, registryOptions())
		for _, t := range registry.ListTools() {
			mode := "write"
			if t.IsReadOnly() {
				mode = "read"
			} else if t.Annotations != nil && t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint {
				mode = "destructive"
			}
			fmt.Printf("%-12s %s\n", mode, t.Name)
		}
		return nil
	}

	if *truenasURL == "" || *apiKey == "" {
		return errors.New("both --truenas-url and --api-key are required (or set TRUENAS_URL and TRUENAS_API_KEY env vars)")
	}

	truenas.SetDebugLogging(*debug)

	// Configure TLS: certificates are verified by default. TrueNAS ships a
	// self-signed certificate, so users must either trust it via --tls-ca
	// or explicitly opt out of verification with --insecure.
	tlsConfig, err := truenas.NewTLSConfig(*insecure, *tlsCA)
	if err != nil {
		return fmt.Errorf("failed to configure TLS: %w", err)
	}
	if *insecure {
		log.Println("WARNING: TLS certificate verification disabled - the connection is vulnerable to man-in-the-middle attacks; prefer --tls-ca with the server's certificate")
	}

	client, err := truenas.NewClient(*truenasURL, *apiKey, tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to create TrueNAS client: %w", err)
	}
	defer client.Close()
	client.SetRequestTimeout(*requestTimeout)

	if err := client.Authenticate(); err != nil {
		return fmt.Errorf("failed to authenticate with TrueNAS: %w", err)
	}
	log.Println("Successfully authenticated with TrueNAS middleware")

	taskManager := tasks.NewManager(client, tasks.PollerConfig{
		PollInterval:    5 * time.Second,
		MaxPollAttempts: 0, // Unlimited
		CleanupInterval: 1 * time.Minute,
	})
	taskManager.Start()
	defer taskManager.Shutdown()

	registry := tools.NewRegistryWithOptions(client, taskManager, registryOptions())
	if *readOnly {
		log.Printf("Read-only mode: exposing %d of %d tools", len(registry.ToolNames()), totalToolCount())
	}

	server := mcp.NewServer(registry, mcp.ServerOptions{
		Name:               "truenas-mcp",
		Version:            Version,
		Instructions:       serverInstructions,
		MaxConcurrentCalls: *maxConcurrent,
		Debug:              *debug,
	})

	// Shut down cleanly on Ctrl-C or SIGTERM so the WebSocket is closed and
	// in-flight tool calls are cancelled rather than abandoned.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *httpAddr != "" {
		return serveHTTP(ctx, server)
	}

	log.Println("Serving MCP over stdio")
	return server.Serve(ctx, mcp.NewStdioTransport())
}

func serveHTTP(ctx context.Context, server *mcp.Server) error {
	token := *httpToken
	if token == "" {
		token = os.Getenv("TRUENAS_MCP_HTTP_TOKEN")
	}
	if token == "" && !mcp.IsLoopback(*httpAddr) {
		// Binding to a LAN address without auth would hand anyone on the
		// network a remote control for the NAS.
		return fmt.Errorf("--http-addr %s is not loopback: set --http-token (or TRUENAS_MCP_HTTP_TOKEN) to require authentication", *httpAddr)
	}
	if token == "" {
		log.Println("WARNING: HTTP transport running without a bearer token (loopback only)")
	}

	handler := mcp.NewHTTPHandler(server, mcp.HTTPOptions{
		Addr:           *httpAddr,
		Path:           *httpPath,
		BearerToken:    token,
		AllowedOrigins: splitList(*httpOrigins),
	})

	httpServer := &http.Server{
		Addr:              *httpAddr,
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("Serving MCP over HTTP on http://%s%s", *httpAddr, *httpPath)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func registryOptions() tools.Options {
	return tools.Options{
		ReadOnly: *readOnly,
		Allow:    splitList(*allowTools),
		Deny:     splitList(*denyTools),
	}
}

func totalToolCount() int {
	return len(tools.NewRegistryWithOptions(nil, nil, tools.Options{}).ToolNames())
}

// applyEnvDefaults fills unset flags from the environment. Flags win: an
// explicit command line should never be overridden by a stale env var.
func applyEnvDefaults() {
	if *truenasURL == "" {
		*truenasURL = os.Getenv("TRUENAS_URL")
	}
	if *apiKey == "" {
		*apiKey = os.Getenv("TRUENAS_API_KEY")
	}
	if *tlsCA == "" {
		*tlsCA = os.Getenv("TRUENAS_TLS_CA")
	}
	if !*debug && truenas.DebugLogging() {
		*debug = true
	}
	if !*insecure && envBool("TRUENAS_INSECURE") {
		*insecure = true
	}
	if !*readOnly && envBool("TRUENAS_MCP_READ_ONLY") {
		*readOnly = true
	}
	if *allowTools == "" {
		*allowTools = os.Getenv("TRUENAS_MCP_ALLOW_TOOLS")
	}
	if *denyTools == "" {
		*denyTools = os.Getenv("TRUENAS_MCP_DENY_TOOLS")
	}
	if *httpAddr == "" {
		*httpAddr = os.Getenv("TRUENAS_MCP_HTTP_ADDR")
	}
}

func envBool(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
