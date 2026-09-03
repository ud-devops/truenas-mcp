package mcp

import "github.com/truenas/truenas-mcp/redact"

// RedactForLog masks credential-bearing values in a JSON-RPC message so that
// debug logging of the wire protocol cannot leak an API key or a bind
// password typed as a tool argument.
func RedactForLog(raw []byte) []byte { return redact.JSON(raw) }
