package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Bootenv tools: boot environment inspection and cleanup.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerBootenvTools() {
	// Boot environment management tools
	r.tools["query_boot_environments"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_boot_environments",
			Description: "Query TrueNAS boot environments with optional filtering and sorting. Returns simplified boot environment information showing which are active/activated/protected/deletable. Use 'limit' to control result size, 'order_by' to sort. Perfect for questions like 'what old boot environments can I clean up?' or 'which boot environment am I running?'",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Filter by boot environment name (partial match)",
					},
					"show_protected_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Show only protected (keep=true) boot environments (default: false)",
					},
					"show_deletable_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Show only boot environments that are safe to delete (default: false)",
					},
					"order_by": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Sort by 'name' (alphabetical), 'created' (newest first, default), or 'size' (largest first)",
						"enum":        []string{"name", "created", "size"},
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Optional: Maximum number of boot environments to return (default: 50 for manageable response size)",
					},
				},
			},
		},
		Handler: handleQueryBootEnvironments,
	}

	r.tools["delete_boot_environment"] = Tool{
		Definition: mcp.Tool{
			Name:        "delete_boot_environment",
			Description: "Delete a boot environment by name. Supports dry-run mode to preview deletion and show warnings. Safety checks prevent deleting active/activated/protected environments. **IMPORTANT**: This operation is permanent and irreversible. Recommend keeping at least 2-3 boot environments for system recovery. Always use dry-run first to verify it is safe to delete.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Required: Boot environment name to delete",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview what will be deleted without executing (default: false)",
						"default":     false,
					},
				},
				"required": []string{"id"},
			},
		},
		Handler: r.handleDeleteBootEnvironmentWithDryRun,
	}

	r.tools["get_current_boot_environment"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_current_boot_environment",
			Description: "Get current boot environment status. Shows which boot environment is currently running (active) and which will boot on next restart (activated). Quick reference tool before making system changes.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleGetCurrentBootEnvironment,
	}
}

// Boot Environment Management Handlers

func handleQueryBootEnvironments(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	// Query all boot environments
	result, err := client.CallContext(ctx, "boot.environment.query", []interface{}{})
	if err != nil {
		return "", fmt.Errorf("failed to query boot environments: %w", err)
	}

	var bootEnvs []map[string]interface{}
	if err := json.Unmarshal(result, &bootEnvs); err != nil {
		return "", fmt.Errorf("failed to parse boot environments: %w", err)
	}

	// Extract filter parameters
	nameFilter, _ := args["name"].(string)
	showProtectedOnly, _ := args["show_protected_only"].(bool)
	showDeletableOnly, _ := args["show_deletable_only"].(bool)
	orderBy, _ := args["order_by"].(string)
	if orderBy == "" {
		orderBy = "created"
	}

	limit := 50
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Track active and activated for metadata
	var activeEnv, activatedEnv string
	var totalSizeBytes int64

	// Simplify and filter boot environments
	simplified := []map[string]interface{}{}
	for _, env := range bootEnvs {
		id, _ := env["id"].(string)

		// Apply name filter
		if nameFilter != "" && !strings.Contains(strings.ToLower(id), strings.ToLower(nameFilter)) {
			continue
		}

		simplifiedEnv := simplifyBootEnvironment(env)

		// Track active and activated environments
		if active, ok := simplifiedEnv["active"].(bool); ok && active {
			activeEnv = id
		}
		if activated, ok := simplifiedEnv["activated"].(bool); ok && activated {
			activatedEnv = id
		}

		// Calculate total size
		if sizeBytes, ok := simplifiedEnv["size_bytes"].(int64); ok {
			totalSizeBytes += sizeBytes
		}

		// Apply protected filter
		if showProtectedOnly {
			if protected, ok := simplifiedEnv["protected"].(bool); !ok || !protected {
				continue
			}
		}

		// Apply deletable filter
		if showDeletableOnly {
			if deletable, ok := simplifiedEnv["deletable"].(bool); !ok || !deletable {
				continue
			}
		}

		simplified = append(simplified, simplifiedEnv)
	}

	// Sort boot environments
	sortBootEnvironments(simplified, orderBy)

	// Apply limit
	if len(simplified) > limit {
		simplified = simplified[:limit]
	}

	// Build metadata wrapper
	filtersApplied := map[string]interface{}{}
	if nameFilter != "" {
		filtersApplied["name"] = nameFilter
	}
	if showProtectedOnly {
		filtersApplied["show_protected_only"] = true
	}
	if showDeletableOnly {
		filtersApplied["show_deletable_only"] = true
	}
	if orderBy != "created" {
		filtersApplied["order_by"] = orderBy
	}

	response := map[string]interface{}{
		"boot_environments":     simplified,
		"count":                 len(simplified),
		"total_count":           len(bootEnvs),
		"active_environment":    activeEnv,
		"activated_environment": activatedEnv,
		"storage_summary": map[string]interface{}{
			"total_size_bytes": totalSizeBytes,
			"total_size_human": formatBytes(totalSizeBytes),
		},
	}

	if len(filtersApplied) > 0 {
		response["filters_applied"] = filtersApplied
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleDeleteBootEnvironment(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return "", fmt.Errorf("id parameter is required")
	}

	// Query all boot environments and find the one to delete
	result, err := client.CallContext(ctx, "boot.environment.query", []interface{}{})
	if err != nil {
		return "", fmt.Errorf("failed to query boot environments: %w", err)
	}

	var bootEnvs []map[string]interface{}
	if err := json.Unmarshal(result, &bootEnvs); err != nil {
		return "", fmt.Errorf("failed to parse boot environments: %w", err)
	}

	// Find the boot environment by ID
	var env map[string]interface{}
	for _, be := range bootEnvs {
		if beID, ok := be["id"].(string); ok && beID == id {
			env = be
			break
		}
	}

	if env == nil {
		return "", fmt.Errorf("boot environment '%s' not found", id)
	}

	// Check safety conditions
	active, _ := env["active"].(bool)
	activated, _ := env["activated"].(bool)
	keep, _ := env["keep"].(bool)

	if active {
		return "", fmt.Errorf("cannot delete active boot environment '%s' (currently running)", id)
	}
	if activated {
		return "", fmt.Errorf("cannot delete activated boot environment '%s' (will boot on next restart)", id)
	}
	if keep {
		return "", fmt.Errorf("cannot delete protected boot environment '%s' (keep flag is set)", id)
	}

	// Get size before deletion
	usedBytes, _ := env["used_bytes"].(float64)
	sizeBytes := int64(usedBytes)

	// Perform deletion
	// TrueNAS API expects parameters as a map
	params := map[string]interface{}{
		"id": id,
	}
	_, err = client.CallContext(ctx, "boot.environment.destroy", params)
	if err != nil {
		return "", fmt.Errorf("failed to delete boot environment: %w", err)
	}

	response := map[string]interface{}{
		"status":      "deleted",
		"id":          id,
		"space_freed": formatBytes(sizeBytes),
		"space_bytes": sizeBytes,
		"message":     fmt.Sprintf("Boot environment '%s' deleted successfully", id),
		"reminder":    "Keep at least 2-3 boot environments for system recovery",
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleGetCurrentBootEnvironment(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	// Query all boot environments
	result, err := client.CallContext(ctx, "boot.environment.query", []interface{}{})
	if err != nil {
		return "", fmt.Errorf("failed to query boot environments: %w", err)
	}

	var bootEnvs []map[string]interface{}
	if err := json.Unmarshal(result, &bootEnvs); err != nil {
		return "", fmt.Errorf("failed to parse boot environments: %w", err)
	}

	var activeEnv, activatedEnv map[string]interface{}

	for _, env := range bootEnvs {
		if active, ok := env["active"].(bool); ok && active {
			activeEnv = simplifyBootEnvironment(env)
		}
		if activated, ok := env["activated"].(bool); ok && activated {
			activatedEnv = simplifyBootEnvironment(env)
		}
	}

	response := map[string]interface{}{
		"active":    activeEnv,
		"activated": activatedEnv,
		"message":   "Active = currently running, Activated = will boot on next restart",
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// Boot Environment Helper Functions

func simplifyBootEnvironment(env map[string]interface{}) map[string]interface{} {
	id, _ := env["id"].(string)
	created, _ := env["created"].(string)
	usedBytes, _ := env["used_bytes"].(float64)
	active, _ := env["active"].(bool)
	activated, _ := env["activated"].(bool)
	keep, _ := env["keep"].(bool)
	canActivate, _ := env["can_activate"].(bool)

	sizeBytes := int64(usedBytes)

	// Parse created timestamp
	var createdTimestamp int64
	if created != "" {
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			createdTimestamp = t.Unix()
		}
	}

	// Determine if deletable
	deletable := !active && !activated && !keep

	// Build deletion blockers
	blockers := []string{}
	if active {
		blockers = append(blockers, "active")
	}
	if activated {
		blockers = append(blockers, "activated")
	}
	if keep {
		blockers = append(blockers, "protected")
	}

	simplified := map[string]interface{}{
		"id":                id,
		"created":           created,
		"created_timestamp": createdTimestamp,
		"size_bytes":        sizeBytes,
		"size_human":        formatBytes(sizeBytes),
		"active":            active,
		"activated":         activated,
		"protected":         keep,
		"can_activate":      canActivate,
		"deletable":         deletable,
		"deletion_blockers": blockers,
	}

	return simplified
}

func sortBootEnvironments(envs []map[string]interface{}, orderBy string) {
	sort.Slice(envs, func(i, j int) bool {
		switch orderBy {
		case "name":
			// Alphabetical by name
			nameI, _ := envs[i]["id"].(string)
			nameJ, _ := envs[j]["id"].(string)
			return nameI < nameJ

		case "size":
			// Largest first
			sizeI, _ := envs[i]["size_bytes"].(int64)
			sizeJ, _ := envs[j]["size_bytes"].(int64)
			return sizeI > sizeJ

		case "created":
			fallthrough
		default:
			// Newest first (highest timestamp)
			tsI, _ := envs[i]["created_timestamp"].(int64)
			tsJ, _ := envs[j]["created_timestamp"].(int64)
			return tsI > tsJ
		}
	})
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	return fmt.Sprintf("%.2f %s", float64(bytes)/float64(div), units[exp])
}

// Dry-run handler for delete boot environment

type deleteBootEnvironmentDryRun struct{}

func (d *deleteBootEnvironmentDryRun) ExecuteDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (*DryRunResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("id parameter is required")
	}

	// Query all boot environments and find the one to delete
	result, err := client.CallContext(ctx, "boot.environment.query", []interface{}{})
	if err != nil {
		return nil, fmt.Errorf("failed to query boot environments: %w", err)
	}

	var bootEnvs []map[string]interface{}
	if err := json.Unmarshal(result, &bootEnvs); err != nil {
		return nil, fmt.Errorf("failed to parse boot environments: %w", err)
	}

	// Find the boot environment by ID
	var env map[string]interface{}
	for _, be := range bootEnvs {
		if beID, ok := be["id"].(string); ok && beID == id {
			env = be
			break
		}
	}

	if env == nil {
		return nil, fmt.Errorf("boot environment '%s' not found", id)
	}

	simplified := simplifyBootEnvironment(env)

	// Check safety conditions
	active, _ := env["active"].(bool)
	activated, _ := env["activated"].(bool)
	keep, _ := env["keep"].(bool)
	usedBytes, _ := env["used_bytes"].(float64)
	sizeBytes := int64(usedBytes)

	deletionAllowed := !active && !activated && !keep

	// Build warnings
	warnings := []string{}

	if !deletionAllowed {
		if active {
			warnings = append(warnings, fmt.Sprintf("BLOCKED: Cannot delete active boot environment '%s' (currently running)", id))
		}
		if activated {
			warnings = append(warnings, fmt.Sprintf("BLOCKED: Cannot delete activated boot environment '%s' (will boot on next restart)", id))
		}
		if keep {
			warnings = append(warnings, fmt.Sprintf("BLOCKED: Cannot delete protected boot environment '%s' (keep flag is set)", id))
		}
	} else {
		warnings = append(warnings, "PERMANENT: This operation cannot be undone")
		warnings = append(warnings, fmt.Sprintf("SPACE: Will free approximately %s", formatBytes(sizeBytes)))
		warnings = append(warnings, "RECOMMENDATION: Keep at least 2-3 boot environments for system recovery")
	}

	// Build planned actions
	actions := []PlannedAction{}
	if deletionAllowed {
		actions = append(actions, PlannedAction{
			Step:        1,
			Description: fmt.Sprintf("Delete boot environment '%s'", id),
			Operation:   "delete",
			Target:      id,
			Details: map[string]interface{}{
				"size_to_free": formatBytes(sizeBytes),
			},
		})
	}

	return &DryRunResult{
		Tool: "delete_boot_environment",
		CurrentState: map[string]interface{}{
			"boot_environment": simplified,
			"deletion_allowed": deletionAllowed,
		},
		PlannedActions: actions,
		Warnings:       warnings,
	}, nil
}

func (r *Registry) handleDeleteBootEnvironmentWithDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return ExecuteWithDryRun(ctx, client, args, &deleteBootEnvironmentDryRun{}, handleDeleteBootEnvironment)
}
