package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// StdioTransport speaks newline-delimited JSON-RPC over a pair of streams,
// which is what MCP clients launch this binary expecting.
//
// Messages are read with a json.Decoder rather than a bufio.Scanner. A Scanner
// caps a token at 64KB by default and then fails the whole stream, so a single
// large tools/call argument (an app config, a long compose file) would kill
// the session with "token too long" and no diagnostic. A Decoder has no such
// limit and additionally tolerates pretty-printed messages that span lines.
type StdioTransport struct {
	dec *json.Decoder
	out io.Writer

	writeMu sync.Mutex

	closeOnce sync.Once
	closed    chan struct{}

	// MaxMessageBytes rejects absurdly large incoming messages. Zero means no
	// limit.
	maxMessageBytes int64
}

// NewStdioTransport reads from os.Stdin and writes to os.Stdout.
func NewStdioTransport() *StdioTransport {
	return NewStdioTransportWith(os.Stdin, os.Stdout, 0)
}

// NewStdioTransportWith builds a transport over arbitrary streams, which is
// what the tests use.
func NewStdioTransportWith(in io.Reader, out io.Writer, maxMessageBytes int64) *StdioTransport {
	return &StdioTransport{
		dec:             json.NewDecoder(bufio.NewReaderSize(in, 64*1024)),
		out:             out,
		closed:          make(chan struct{}),
		maxMessageBytes: maxMessageBytes,
	}
}

func (t *StdioTransport) Read() ([]byte, error) {
	select {
	case <-t.closed:
		return nil, io.EOF
	default:
	}

	var raw json.RawMessage
	if err := t.dec.Decode(&raw); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		select {
		case <-t.closed:
			return nil, io.EOF
		default:
		}
		return nil, fmt.Errorf("stdin decode error: %w", err)
	}

	if t.maxMessageBytes > 0 && int64(len(raw)) > t.maxMessageBytes {
		return nil, fmt.Errorf("message of %d bytes exceeds limit of %d", len(raw), t.maxMessageBytes)
	}
	return raw, nil
}

func (t *StdioTransport) Write(data []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	// One write for the payload and its delimiter: a client framing on
	// newlines must never observe a half-written line.
	buf := make([]byte, 0, len(data)+1)
	buf = append(buf, data...)
	buf = append(buf, '\n')

	if _, err := t.out.Write(buf); err != nil {
		return fmt.Errorf("failed to write to stdout: %w", err)
	}
	if f, ok := t.out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
	return nil
}

func (t *StdioTransport) Close() error {
	t.closeOnce.Do(func() { close(t.closed) })
	return nil
}
