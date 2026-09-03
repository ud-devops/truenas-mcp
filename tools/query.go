package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/truenas/truenas-mcp/truenas"
)

// queryOptions describes a straightforward "query a middleware collection and
// summarise it" tool.
//
// Most read-only tools differ only in which method they call, which fields are
// worth showing, and how the result is labelled. Expressing that as data keeps
// each new tool to a few lines instead of another hundred-line copy of the
// same filter/limit/marshal dance.
type queryOptions struct {
	// Method is the middleware method, e.g. "user.query".
	Method string
	// Label names the collection in the response, e.g. "users".
	Label string
	// Fields, when non-empty, restricts each record to these keys. Nested
	// paths are not supported; a missing key is simply omitted.
	Fields []string
	// DefaultLimit caps results when the caller does not ask for a limit.
	DefaultLimit int
	// Filters builds middleware query-filters from the tool arguments.
	Filters func(args map[string]interface{}) []interface{}
}

// runQuery executes a collection query and returns a JSON summary.
func runQuery(ctx context.Context, client *truenas.Client, args map[string]interface{}, opts queryOptions) (string, error) {
	filters := []interface{}{}
	if opts.Filters != nil {
		if f := opts.Filters(args); f != nil {
			filters = f
		}
	}

	raw, err := client.CallContext(ctx, opts.Method, filters, map[string]interface{}{})
	if err != nil {
		return "", err
	}

	var records []map[string]interface{}
	if err := json.Unmarshal(raw, &records); err != nil {
		return "", fmt.Errorf("failed to parse %s response: %w", opts.Method, err)
	}

	total := len(records)
	limit := intArg(args, "limit", opts.DefaultLimit)
	truncated := false
	if limit > 0 && len(records) > limit {
		records = records[:limit]
		truncated = true
	}

	items := make([]map[string]interface{}, 0, len(records))
	for _, rec := range records {
		items = append(items, projectFields(rec, opts.Fields))
	}

	result := map[string]interface{}{
		opts.Label:      items,
		"total":         total,
		"returned":      len(items),
		"truncated":     truncated,
		"source_method": opts.Method,
	}
	return marshalJSON(result)
}

// projectFields keeps only the named keys, or everything when none are named.
func projectFields(rec map[string]interface{}, fields []string) map[string]interface{} {
	if len(fields) == 0 {
		return rec
	}
	out := make(map[string]interface{}, len(fields))
	for _, f := range fields {
		if v, ok := rec[f]; ok {
			out[f] = v
		}
	}
	return out
}

// intArg reads a numeric argument, tolerating the float64 that JSON decoding
// always produces.
func intArg(args map[string]interface{}, name string, fallback int) int {
	switch v := args[name].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return fallback
}

func stringArg(args map[string]interface{}, name string) string {
	s, _ := args[name].(string)
	return s
}

func boolArg(args map[string]interface{}, name string) bool {
	b, _ := args[name].(bool)
	return b
}

// containsFilter builds the middleware's case-insensitive substring filter.
func containsFilter(field, value string) []interface{} {
	return []interface{}{field, "~", "(?i)" + value}
}

// limitSchema is the shared "limit" property, so every query tool documents it
// the same way.
func limitSchema(def int) map[string]interface{} {
	return map[string]interface{}{
		"type":        "integer",
		"description": fmt.Sprintf("Maximum number of results to return (default %d)", def),
	}
}
