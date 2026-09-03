package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/tasks"
	"github.com/truenas/truenas-mcp/truenas"
)

// HandlerFunc executes one tool.
//
// The context is cancelled when the MCP client cancels the request or the
// server shuts down; handlers pass it to client.CallContext so an abandoned
// call stops occupying a middleware slot.
type HandlerFunc func(context.Context, *truenas.Client, map[string]interface{}) (string, error)

type Registry struct {
	client      *truenas.Client
	taskManager *tasks.Manager
	tools       map[string]Tool
	opts        Options
}

type Tool struct {
	Definition mcp.Tool
	Handler    HandlerFunc
}

// Options controls which tools a registry exposes.
type Options struct {
	// ReadOnly hides and refuses every tool not annotated as read-only. It is
	// the difference between "let an assistant look at my NAS" and "let an
	// assistant reboot my NAS", and it should be the default for anyone
	// pointing a model at production storage.
	ReadOnly bool
	// Allow, when non-empty, restricts the exposed set to these tool names.
	Allow []string
	// Deny removes specific tool names. Applied after Allow.
	Deny []string
}

func NewRegistry(client *truenas.Client, taskManager *tasks.Manager) *Registry {
	return NewRegistryWithOptions(client, taskManager, Options{})
}

// NewRegistryWithOptions builds a registry exposing only the tools permitted
// by opts.
func NewRegistryWithOptions(client *truenas.Client, taskManager *tasks.Manager, opts Options) *Registry {
	r := &Registry{
		client:      client,
		taskManager: taskManager,
		tools:       make(map[string]Tool),
		opts:        opts,
	}
	r.registerTools()
	r.applyAnnotations()
	r.applyFilters()
	return r
}

// registerTools populates the tool table. Each domain registers its own
// tools from domain_<name>.go.
func (r *Registry) registerTools() {
	r.registerAlertsTools()
	r.registerAppsTools()
	r.registerBootenvTools()
	r.registerCapacityTools()
	r.registerDirectoryTools()
	r.registerInfraTools()
	r.registerJobsTools()
	r.registerMetricsTools()
	r.registerScrubTools()
	r.registerSnapshotsTools()
	r.registerStorageTools()
	r.registerSystemTools()
	r.registerTasksTools()
	r.registerUpdatesTools()
	r.registerVmsTools()
}

// applyAnnotations attaches behavioural hints to every registered tool.
//
// Keeping the table here rather than beside each definition makes the safety
// classification auditable in one place: a reviewer can see at a glance which
// tools this server considers destructive, and an unlisted tool is treated as
// destructive by default rather than silently becoming callable in read-only
// mode.
func (r *Registry) applyAnnotations() {
	for name, tool := range r.tools {
		ann, ok := toolAnnotations[name]
		if !ok {
			// Unclassified: assume the worst.
			ann = mcp.ToolAnnotations{
				ReadOnlyHint:    mcp.Ptr(false),
				DestructiveHint: mcp.Ptr(true),
			}
		}
		copied := ann
		tool.Definition.Annotations = &copied
		r.tools[name] = tool
	}
}

func (r *Registry) applyFilters() {
	if len(r.opts.Allow) > 0 {
		allow := toSet(r.opts.Allow)
		for name := range r.tools {
			if !allow[name] {
				delete(r.tools, name)
			}
		}
	}
	for _, name := range r.opts.Deny {
		delete(r.tools, strings.TrimSpace(name))
	}
	if r.opts.ReadOnly {
		for name, tool := range r.tools {
			if !tool.Definition.IsReadOnly() {
				delete(r.tools, name)
			}
		}
	}
}

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			set[v] = true
		}
	}
	return set
}

// ListTools returns the exposed tools in a stable order.
//
// Map iteration order is random in Go, so the previous implementation returned
// tools/list in a different order on every call. That defeats client-side
// caching and makes diffing two servers' capabilities needlessly painful.
func (r *Registry) ListTools() []mcp.Tool {
	tools := make([]mcp.Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool.Definition)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// ToolNames returns the exposed tool names, sorted.
func (r *Registry) ToolNames() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	tool, exists := r.tools[name]
	if !exists {
		if _, blocked := allToolNames[name]; blocked {
			return "", fmt.Errorf("tool %q is not available in this server's configuration (read-only mode or an allow/deny list is in effect)", name)
		}
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	// Defence in depth: filtering already removed write tools in read-only
	// mode, but a future registration path must not be able to slip past it.
	if r.opts.ReadOnly && !tool.Definition.IsReadOnly() {
		return "", fmt.Errorf("tool %q modifies the system and this server is running in read-only mode", name)
	}

	if err := validateArguments(tool.Definition, args); err != nil {
		return "", err
	}

	return tool.Handler(ctx, r.client, args)
}

// allToolNames is the unfiltered universe of tools, used only to give a
// clearer error when a tool exists but has been filtered out.
var allToolNames = func() map[string]struct{} {
	r := &Registry{tools: make(map[string]Tool)}
	r.registerTools()
	set := make(map[string]struct{}, len(r.tools))
	for name := range r.tools {
		set[name] = struct{}{}
	}
	return set
}()
