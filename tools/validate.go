package tools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/truenas/truenas-mcp/mcp"
)

// validateArguments checks a tool call against the tool's declared input
// schema before the handler runs.
//
// Without this, a model that omits a required argument or passes a string
// where a number belongs reaches the handler, which then either panics on a
// failed type assertion or forwards nonsense to the TrueNAS middleware and
// surfaces a stack trace. Failing here instead produces an error the model can
// actually act on: it names the argument and what was expected.
func validateArguments(def mcp.Tool, args map[string]interface{}) error {
	schema := def.InputSchema
	if schema == nil {
		return nil
	}

	var problems []string

	for _, name := range requiredFields(schema) {
		if v, ok := args[name]; !ok || v == nil {
			problems = append(problems, fmt.Sprintf("missing required argument %q", name))
		}
	}

	props, _ := schema["properties"].(map[string]interface{})
	if props != nil {
		names := make([]string, 0, len(args))
		for name := range args {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			value := args[name]
			if value == nil {
				continue
			}
			spec, ok := props[name].(map[string]interface{})
			if !ok {
				continue
			}
			if err := checkValue(name, value, spec); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid arguments for %s: %s", def.Name, strings.Join(problems, "; "))
}

// requiredFields reads the schema's "required" list, which definitions in this
// package write as []string while JSON-decoded schemas produce []interface{}.
func requiredFields(schema map[string]interface{}) []string {
	switch req := schema["required"].(type) {
	case []string:
		return req
	case []interface{}:
		out := make([]string, 0, len(req))
		for _, v := range req {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func checkValue(name string, value interface{}, spec map[string]interface{}) error {
	wantType, _ := spec["type"].(string)
	if wantType != "" && !matchesType(value, wantType) {
		return fmt.Errorf("argument %q expects %s, got %s", name, wantType, goTypeName(value))
	}
	if enum := enumValues(spec); len(enum) > 0 {
		got, ok := value.(string)
		if ok {
			for _, allowed := range enum {
				if allowed == got {
					return nil
				}
			}
			return fmt.Errorf("argument %q must be one of [%s], got %q", name, strings.Join(enum, ", "), got)
		}
	}
	return nil
}

func matchesType(value interface{}, want string) bool {
	switch want {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		return isNumber(value)
	case "integer":
		// JSON has no integer type: every number arrives as float64, so an
		// integral float is a valid integer.
		switch v := value.(type) {
		case float64:
			return v == float64(int64(v))
		case int, int32, int64:
			return true
		}
		return false
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	}
	return true
}

func isNumber(value interface{}) bool {
	switch value.(type) {
	case float64, float32, int, int32, int64:
		return true
	}
	return false
}

func enumValues(spec map[string]interface{}) []string {
	switch e := spec["enum"].(type) {
	case []string:
		return e
	case []interface{}:
		out := make([]string, 0, len(e))
		for _, v := range e {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func goTypeName(v interface{}) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, float32, int, int32, int64:
		return "number"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}
