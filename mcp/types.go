package mcp

import (
	"context"
	"encoding/json"
)

// JSON-RPC 2.0 message types

// Request is an incoming JSON-RPC message. A message without an ID is a
// notification and must not be answered.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the message carries no ID and therefore
// expects no response.
func (r *Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// JSON-RPC and MCP error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// MCP-specific types

type InitializeParams struct {
	ProtocolVersion string      `json:"protocolVersion"`
	ClientInfo      ServerInfo  `json:"clientInfo"`
	Capabilities    interface{} `json:"capabilities,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
	Capabilities    Capabilities `json:"capabilities"`
	Instructions    string       `json:"instructions,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Capabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// ToolAnnotations are optional behavioural hints about a tool. They let a
// client (and this server's own read-only guard) reason about whether calling
// a tool can change or destroy state, without having to parse its description.
//
// The pointer fields distinguish "not stated" from "explicitly false", which
// matters: an unannotated tool is treated as potentially destructive.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Annotations *ToolAnnotations       `json:"annotations,omitempty"`
}

// IsReadOnly reports whether the tool is annotated as making no changes.
// Tools with no annotation are conservatively treated as not read-only.
func (t Tool) IsReadOnly() bool {
	return t.Annotations != nil && t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint
}

type ToolsListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type ToolsListResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CancelledParams is the payload of a notifications/cancelled message.
type CancelledParams struct {
	RequestID json.RawMessage `json:"requestId"`
	Reason    string          `json:"reason,omitempty"`
}

// ToolRegistry interface for tool management.
//
// CallTool receives a context that is cancelled when the client cancels the
// request or the connection goes away; handlers are expected to honour it so
// an abandoned call does not keep a TrueNAS job slot busy.
type ToolRegistry interface {
	ListTools() []Tool
	CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error)
}

// Ptr returns a pointer to v. Useful for the optional annotation fields.
func Ptr[T any](v T) *T { return &v }
