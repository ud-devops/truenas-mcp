package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/truenas/truenas-mcp/mcp"
)

func TestListToolsIsSorted(t *testing.T) {
	r := NewRegistry(nil, nil)

	// Map iteration is randomised, so a single pass could pass by luck.
	for i := 0; i < 20; i++ {
		tools := r.ListTools()
		for j := 1; j < len(tools); j++ {
			if tools[j-1].Name > tools[j].Name {
				t.Fatalf("tools/list is not sorted: %q before %q", tools[j-1].Name, tools[j].Name)
			}
		}
	}
}

func TestEveryToolIsAnnotated(t *testing.T) {
	r := NewRegistry(nil, nil)
	for _, tool := range r.ListTools() {
		if tool.Annotations == nil {
			t.Errorf("%s has no annotations", tool.Name)
			continue
		}
		if _, ok := toolAnnotations[tool.Name]; !ok {
			t.Errorf("%s is missing from the annotation table, so it defaults to destructive", tool.Name)
		}
	}
}

func TestReadOnlyModeHidesWriteTools(t *testing.T) {
	full := NewRegistry(nil, nil)
	ro := NewRegistryWithOptions(nil, nil, Options{ReadOnly: true})

	if len(ro.ToolNames()) == 0 {
		t.Fatal("read-only mode exposed no tools at all")
	}
	if len(ro.ToolNames()) >= len(full.ToolNames()) {
		t.Fatal("read-only mode did not remove anything")
	}

	for _, tool := range ro.ListTools() {
		if !tool.IsReadOnly() {
			t.Errorf("%s is exposed in read-only mode but is not annotated read-only", tool.Name)
		}
	}

	for _, name := range []string{"system_reboot", "delete_app", "apply_update", "create_dataset"} {
		if _, err := ro.CallTool(context.Background(), name, nil); err == nil {
			t.Errorf("%s was callable in read-only mode", name)
		}
	}
}

func TestReadOnlyErrorExplainsWhy(t *testing.T) {
	ro := NewRegistryWithOptions(nil, nil, Options{ReadOnly: true})
	_, err := ro.CallTool(context.Background(), "system_reboot", nil)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("error = %v, want it to name read-only mode", err)
	}
}

func TestAllowAndDenyLists(t *testing.T) {
	r := NewRegistryWithOptions(nil, nil, Options{Allow: []string{"system_info", "query_pools"}})
	if got := r.ToolNames(); len(got) != 2 {
		t.Fatalf("allowlist produced %v, want 2 tools", got)
	}

	r = NewRegistryWithOptions(nil, nil, Options{Deny: []string{"system_reboot"}})
	for _, name := range r.ToolNames() {
		if name == "system_reboot" {
			t.Fatal("denied tool is still exposed")
		}
	}
	if _, err := r.CallTool(context.Background(), "system_reboot", nil); err == nil {
		t.Fatal("denied tool was callable")
	}
}

func TestUnknownToolError(t *testing.T) {
	r := NewRegistry(nil, nil)
	_, err := r.CallTool(context.Background(), "no_such_tool", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("error = %v, want unknown tool", err)
	}
}

func TestValidateArguments(t *testing.T) {
	def := mcp.Tool{
		Name: "demo",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":     map[string]interface{}{"type": "string"},
				"limit":    map[string]interface{}{"type": "integer"},
				"force":    map[string]interface{}{"type": "boolean"},
				"order_by": map[string]interface{}{"type": "string", "enum": []string{"name", "size"}},
			},
			"required": []string{"name"},
		},
	}

	tests := []struct {
		desc    string
		args    map[string]interface{}
		wantErr string
	}{
		{"valid", map[string]interface{}{"name": "tank", "limit": float64(10)}, ""},
		{"missing required", map[string]interface{}{"limit": float64(1)}, `missing required argument "name"`},
		{"wrong type", map[string]interface{}{"name": 42}, `expects string`},
		{"non-integral integer", map[string]interface{}{"name": "t", "limit": 1.5}, `expects integer`},
		{"bad enum", map[string]interface{}{"name": "t", "order_by": "colour"}, `must be one of`},
		{"good enum", map[string]interface{}{"name": "t", "order_by": "size"}, ""},
		{"bool ok", map[string]interface{}{"name": "t", "force": true}, ""},
		{"bool wrong", map[string]interface{}{"name": "t", "force": "yes"}, `expects boolean`},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			err := validateArguments(def, tc.args)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// Required arguments are checked before the handler runs, so a call that used
// to reach TrueNAS (or panic on a type assertion) now fails with a message the
// model can act on.
func TestCallToolValidatesBeforeDispatch(t *testing.T) {
	r := NewRegistry(nil, nil)
	_, err := r.CallTool(context.Background(), "tasks_get", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "task_id") {
		t.Fatalf("error = %v, want it to name the missing task_id argument", err)
	}
}

func TestSchemasAreWellFormed(t *testing.T) {
	r := NewRegistry(nil, nil)
	for _, tool := range r.ListTools() {
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("%s has no input schema", tool.Name)
			continue
		}
		if typ, _ := tool.InputSchema["type"].(string); typ != "object" {
			t.Errorf("%s input schema type = %q, want object", tool.Name, typ)
		}
		props, ok := tool.InputSchema["properties"].(map[string]interface{})
		if !ok {
			t.Errorf("%s has no properties map", tool.Name)
			continue
		}
		for _, name := range requiredFields(tool.InputSchema) {
			if _, ok := props[name]; !ok {
				t.Errorf("%s requires %q but does not declare it as a property", tool.Name, name)
			}
		}
	}
}
