package truenas

import (
	"strings"
	"testing"
)

func TestBuildConnectionURLs(t *testing.T) {
	tests := []struct {
		endpoint string
		want     string
	}{
		{"truenas.local", "wss://truenas.local:443/websocket"},
		{"10.0.0.1", "wss://10.0.0.1:443/websocket"},

		// A port the operator chose must survive. The previous implementation
		// stripped everything after the last colon and hard-coded :443, so a
		// TrueNAS behind a reverse proxy on 8443 was unreachable.
		{"truenas.local:8443", "wss://truenas.local:8443/websocket"},
		{"10.0.0.1:8443", "wss://10.0.0.1:8443/websocket"},

		// Bare IPv6 was truncated at the last colon ("fd00::1" -> "fd00:").
		{"fd00::1", "wss://[fd00::1]:443/websocket"},
		{"[fd00::1]", "wss://[fd00::1]:443/websocket"},
		{"[fd00::1]:8443", "wss://[fd00::1]:8443/websocket"},
		{"::1", "wss://[::1]:443/websocket"},

		{"wss://truenas.local/websocket", "wss://truenas.local/websocket"},
		{"wss://truenas.local:8443/websocket", "wss://truenas.local:8443/websocket"},
		{"wss://truenas.local", "wss://truenas.local/websocket"},
		{"https://truenas.local", "wss://truenas.local/websocket"},
	}

	for _, tc := range tests {
		t.Run(tc.endpoint, func(t *testing.T) {
			c := &Client{endpoint: tc.endpoint}
			urls, err := c.buildConnectionURLs()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(urls) != 1 || urls[0] != tc.want {
				t.Fatalf("got %v, want [%s]", urls, tc.want)
			}
		})
	}
}

func TestBuildConnectionURLsRejectsPlaintext(t *testing.T) {
	for _, endpoint := range []string{"ws://truenas.local/websocket", "http://truenas.local"} {
		c := &Client{endpoint: endpoint}
		_, err := c.buildConnectionURLs()
		if err == nil {
			t.Fatalf("%s was accepted; TrueNAS revokes API keys used over plaintext", endpoint)
		}
		if !strings.Contains(err.Error(), "SECURITY") {
			t.Errorf("error for %s = %v, want it to flag the security problem", endpoint, err)
		}
	}
}

func TestBuildConnectionURLsRejectsEmpty(t *testing.T) {
	c := &Client{endpoint: "   "}
	if _, err := c.buildConnectionURLs(); err == nil {
		t.Fatal("empty endpoint was accepted")
	}
}

func TestRequestTimeoutDefaultsAndOverrides(t *testing.T) {
	c := &Client{}
	if got := c.requestTimeout(); got != DefaultRequestTimeout {
		t.Fatalf("default timeout = %s, want %s", got, DefaultRequestTimeout)
	}

	c.SetRequestTimeout(5 * 1e9)
	if got := c.requestTimeout(); got != 5*1e9 {
		t.Fatalf("timeout = %s, want 5s", got)
	}

	c.SetRequestTimeout(0)
	if got := c.requestTimeout(); got != DefaultRequestTimeout {
		t.Fatalf("zero did not restore the default, got %s", got)
	}
}
