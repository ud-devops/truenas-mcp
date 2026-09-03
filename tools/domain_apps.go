package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Apps tools: application lifecycle and catalog.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerAppsTools() {
	// Query installed apps
	r.tools["query_apps"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_apps",
			Description: "Query installed applications with their status, versions, and available updates",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Filter by specific app name",
					},
					"include_config": map[string]interface{}{
						"type":        "boolean",
						"description": "Include app configuration details (default: false)",
						"default":     false,
					},
				},
			},
		},
		Handler: handleQueryApps,
	}

	// Upgrade app
	r.tools["upgrade_app"] = Tool{
		Definition: mcp.Tool{
			Name:        "upgrade_app",
			Description: "Upgrade an application to a newer version. Supports dry-run mode to preview changes. Returns a task ID for tracking progress. This is a write operation that modifies the system.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the application to upgrade",
					},
					"version": map[string]interface{}{
						"type":        "string",
						"description": "Target version to upgrade to (default: 'latest')",
						"default":     "latest",
					},
					"snapshot_hostpaths": map[string]interface{}{
						"type":        "boolean",
						"description": "Create snapshots of host volumes before upgrade (default: true for safety)",
						"default":     true,
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview changes without executing (default: false)",
						"default":     false,
					},
				},
				"required": []string{"app_name"},
			},
		},
		Handler: r.handleUpgradeAppWithDryRun,
	}

	// Start app
	r.tools["start_app"] = Tool{
		Definition: mcp.Tool{
			Name:        "start_app",
			Description: "Start a stopped TrueNAS application. Job-based; use tasks_get with returned task_id to track progress. Supports dry_run to preview the action without executing it.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the application to start",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview the action without executing it (default: false)",
						"default":     false,
					},
				},
				"required": []string{"app_name"},
			},
		},
		Handler: r.handleStartAppWithDryRun,
	}

	// Stop app
	r.tools["stop_app"] = Tool{
		Definition: mcp.Tool{
			Name:        "stop_app",
			Description: "Stop a running TrueNAS application. Job-based; use tasks_get with returned task_id to track progress. Supports dry_run to preview the action without executing it.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the application to stop",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview the action without executing it (default: false)",
						"default":     false,
					},
				},
				"required": []string{"app_name"},
			},
		},
		Handler: r.handleStopAppWithDryRun,
	}

	// Get app config
	r.tools["get_app_config"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_app_config",
			Description: "Retrieve the current user-specified configuration for an installed app. Returns the values object that can be modified and passed to update_app.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the installed application",
					},
				},
				"required": []string{"app_name"},
			},
		},
		Handler: handleGetAppConfig,
	}

	// Update app
	r.tools["update_app"] = Tool{
		Definition: mcp.Tool{
			Name:        "update_app",
			Description: "Update an installed app's configuration. Job-based; use tasks_get with returned task_id to track progress. Use get_app_config first to retrieve current config, then pass modified values. Supports dry_run to preview changes. This is a write operation that modifies the system.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the installed application to update",
					},
					"values": map[string]interface{}{
						"type":        "object",
						"description": "Updated configuration values. Use get_app_config to retrieve current values first. Storage must use host_path (not ix_volume).",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview changes without executing (default: false)",
						"default":     false,
					},
				},
				"required": []string{"app_name", "values"},
			},
		},
		Handler: r.handleUpdateAppWithDryRun,
	}

	// Search app catalog
	r.tools["search_app_catalog"] = Tool{
		Definition: mcp.Tool{
			Name:        "search_app_catalog",
			Description: "Search TrueNAS app catalog by name, category, or keyword. Returns available applications from the catalog with their versions, categories, and installation status.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query (partial match on name or description)",
					},
					"train": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"stable", "enterprise", "community", "all"},
						"description": "Filter by catalog train (default: stable)",
						"default":     "stable",
					},
					"category": map[string]interface{}{
						"type":        "string",
						"description": "Filter by category (e.g., 'media', 'productivity', 'database')",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum results to return (default: 20)",
						"default":     20,
					},
				},
			},
		},
		Handler: handleSearchAppCatalog,
	}

	// Get app catalog details
	r.tools["get_app_catalog_details"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_app_catalog_details",
			Description: "Get detailed information about a specific app from the catalog including README, screenshots, version info, and storage volume hints. Use this after searching to understand an app's requirements before installation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type":        "string",
						"description": "App name from catalog (from search results)",
					},
					"train": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"stable", "enterprise", "community"},
						"description": "Catalog train (default: stable)",
						"default":     "stable",
					},
				},
				"required": []string{"app_name"},
			},
		},
		Handler: handleGetAppCatalogDetails,
	}

	// Install app
	r.tools["install_app"] = Tool{
		Definition: mcp.Tool{
			Name: "install_app",
			Description: `Install a TrueNAS application using schema-driven configuration.

**IMPORTANT: ALL TRUENAS APPS ARE COMPLEX**
Every app requires configuration across multiple groups (currently 6, but may vary):
1. App Configuration (timezone, app-specific settings)
2. User and Group Configuration (run_as user/group IDs)
3. Network Configuration (ports and networking)
4. Storage Configuration (volumes and datasets)
5. Labels Configuration (metadata labels)
6. Resources Configuration (CPU, memory, GPU)

**UNIVERSAL WIZARD - SECTION-BY-SECTION CONFIGURATION:**

**STEP 1: Get App Schema**
1. Call get_app_catalog_details(app_name, train)
2. Review schema.groups array (iterate through ALL groups, don't assume count)
3. Check schema.group_count to know how many groups to configure
4. Review schema.questions_by_group (shows questions for each group)
5. Review wizard_guidance for common patterns

**STEP 2: Understand Common Patterns**

All apps follow these patterns:

• **Timezone** (Group 1):
  - Variable: TZ
  - Type: enum with 600+ timezones
  - Recommendation: Use "Etc/UTC" or user's timezone

• **User/Group** (Group 2):
  - Variable: run_as
  - Structure: {user: <uid>, group: <gid>}
  - Default: {user: 568, group: 568} (apps user/group)

• **Network** (Group 3):
  - Variable: network
  - Ports: {bind_mode: "published", port_number: <port>, host_ips: []}
  - Common ports: web_port, api_port, sync_port, etc.
  - bind_mode: "published" (external) or "exposed" (internal) or "" (none)

• **Storage** (Group 4) - CRITICAL:
  - Variable: storage
  - ALWAYS use: {"type": "host_path", "host_path_config": {"path": "/mnt/...", "acl_enable": false}}
  - NEVER use: {"type": "ix_volume", ...}
  - Common volumes: config, cache, data, transcodes
  - Pattern: /mnt/<pool>/apps/<appname>/<volume>

• **Labels** (Group 5):
  - Variable: labels
  - Structure: [{key: "name", value: "value"}]
  - Usually optional (empty array)

• **Resources** (Group 6):
  - Variable: resources
  - Structure: {limits: {cpus: 2, memory: 4096}, gpus: {...}}
  - Defaults: 2 CPUs, 4096 MB RAM

**STEP 3: Plan Storage (CRITICAL - Do This First)**

1. Identify storage volumes from schema:
   - Look in schema.questions_by_group["Storage Configuration"]
   - Find variables like: config, cache, data, transcodes, additional_storage
   - Each has type enum: ["host_path", "ix_volume", ...]

2. Call query_pools() to find available pools

3. Recommend dataset structure:
   - Format: <pool>/apps/<appname>/<volume>
   - Example: tank/apps/jellyfin/config

4. Present plan to user:
   "I'll create the following datasets for Jellyfin:
    - tank/apps/jellyfin/config (10GB)
    - tank/apps/jellyfin/cache (50GB)
    - tank/apps/jellyfin/transcodes (temporary, no dataset needed)"

**STEP 4: Create Datasets**

For each permanent storage volume (not temporary/tmpfs):
1. Call create_dataset with:
   - name: "<pool>/apps/<appname>/<volume>"
   - type: "FILESYSTEM"
   - share_type: "APPS"
   - compression: "LZ4"
   - quota: <size_in_bytes> (optional)
2. Confirm creation
3. Recommended quotas:
   - config: 10GB (10737418240)
   - cache: 50GB (53687091200)
   - data: 1TB+ (varies by app)

**STEP 5: Build Configuration by Group**

Go through each group and build configuration:

**Group 1 - App Configuration:**
{
  "TZ": "Etc/UTC",
  "<appname>": {
    // App-specific settings from schema
    "additional_envs": []
  }
}

**Group 2 - User/Group:**
{
  "run_as": {
    "user": 568,
    "group": 568
  }
}

**Group 3 - Network:**
{
  "network": {
    "web_port": {
      "bind_mode": "published",
      "port_number": 30013,
      "host_ips": []
    },
    "host_network": false
  }
}

**Group 4 - Storage (CRITICAL):**
{
  "storage": {
    "config": {
      "type": "host_path",
      "host_path_config": {
        "path": "/mnt/tank/apps/jellyfin/config",
        "acl_enable": false
      }
    },
    "cache": {
      "type": "host_path",
      "host_path_config": {
        "path": "/mnt/tank/apps/jellyfin/cache",
        "acl_enable": false
      }
    },
    "transcodes": {
      "type": "temporary"
    },
    "additional_storage": []
  }
}

**Group 5 - Labels:**
{
  "labels": []
}

**Group 6 - Resources:**
{
  "resources": {
    "limits": {
      "cpus": 2,
      "memory": 4096
    },
    "gpus": {}
  }
}

**STEP 6: Assemble Complete Values Object**

Combine all groups into single values object:
{
  "TZ": "Etc/UTC",
  "jellyfin": {...},
  "run_as": {...},
  "network": {...},
  "storage": {...},
  "labels": [...],
  "resources": {...}
}

**STEP 7: Validate Configuration**

1. All storage volumes use type="host_path"
2. All paths start with /mnt/
3. All required groups present
4. Port numbers in valid range (1-65535)
5. User/group IDs are valid (>= 0)

**STEP 8: Dry-Run Preview**

Call install_app with dry_run=true:
install_app(
  app_name="jellyfin",
  catalog_app="jellyfin",
  train="community",
  values={...complete config...},
  dry_run=true
)

Review:
- Datasets exist?
- Configuration valid?
- Warnings or errors?

**STEP 9: Execute Installation**

If dry-run successful, call with dry_run=false:
install_app(
  app_name="jellyfin",
  catalog_app="jellyfin",
  train="community",
  values={...complete config...},
  dry_run=false
)

Returns task_id for tracking progress with tasks_get.

**CRITICAL SAFETY RULES:**
- ALWAYS use "type": "host_path" for storage
- NEVER use "type": "ix_volume"
- ALWAYS create datasets before installation
- ALWAYS validate paths start with /mnt/
- ALWAYS use dry-run before final installation

**ERROR RECOVERY:**
- Missing datasets: Create with create_dataset
- ix_volume detected: Convert to host_path format
- Invalid structure: Review schema and rebuild section
- Validation failed: Check error message for exact location`,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type":        "string",
						"description": "Unique app instance name (lowercase, alphanumeric, hyphens, 1-40 chars). Pattern: ^[a-z]([-a-z0-9]*[a-z0-9])?$",
						"pattern":     "^[a-z]([-a-z0-9]*[a-z0-9])?$",
					},
					"catalog_app": map[string]interface{}{
						"type":        "string",
						"description": "Catalog app name (from search results)",
					},
					"train": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"stable", "enterprise", "community"},
						"description": "Catalog train (default: stable)",
						"default":     "stable",
					},
					"version": map[string]interface{}{
						"type":        "string",
						"description": "App version (default: latest)",
						"default":     "latest",
					},
					"values": map[string]interface{}{
						"type":        "object",
						"description": "Complete app configuration assembled from schema groups. Includes TZ, run_as, network, storage (host_path only), labels, and resources. Build this by iterating through schema groups from get_app_catalog_details.",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview installation without executing (default: false)",
						"default":     false,
					},
				},
				"required": []string{"app_name", "catalog_app", "values"},
			},
		},
		Handler: r.handleInstallAppWithDryRun,
	}

	// Delete app
	r.tools["delete_app"] = Tool{
		Definition: mcp.Tool{
			Name:        "delete_app",
			Description: "Remove an installed application. IMPORTANT: Host-path datasets are NOT deleted and must be manually removed after app deletion. Data will be preserved in original locations. Use dry-run mode to preview what will be deleted.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"app_name": map[string]interface{}{
						"type":        "string",
						"description": "Installed app instance name to delete",
					},
					"remove_images": map[string]interface{}{
						"type":        "boolean",
						"description": "Remove container images (default: false)",
						"default":     false,
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview deletion without executing (default: false)",
						"default":     false,
					},
				},
				"required": []string{"app_name"},
			},
		},
		Handler: r.handleDeleteAppWithDryRun,
	}
}

func handleQueryApps(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, _ := args["app_name"].(string)
	includeConfig, _ := args["include_config"].(bool)

	// Build query filters and options
	// Initialize as empty array, not nil (API expects [] not null)
	filters := []interface{}{}
	if appName != "" {
		filters = []interface{}{
			[]interface{}{"name", "=", appName},
		}
	}

	options := map[string]interface{}{
		"extra": map[string]interface{}{
			"retrieve_config": includeConfig,
		},
	}

	result, err := client.CallContext(ctx, "app.query", filters, options)
	if err != nil {
		return "", fmt.Errorf("failed to query apps: %w", err)
	}

	var apps []map[string]interface{}
	if err := json.Unmarshal(result, &apps); err != nil {
		return "", fmt.Errorf("failed to parse app list: %w", err)
	}

	// Simplify the response to show most relevant information
	simplified := make([]map[string]interface{}, 0, len(apps))
	for _, app := range apps {
		summary := map[string]interface{}{
			"name":              app["name"],
			"id":                app["id"],
			"state":             app["state"],
			"version":           app["human_version"],
			"upgrade_available": app["upgrade_available"],
		}

		// Include update info if available
		if upgradeAvail, ok := app["upgrade_available"].(bool); ok && upgradeAvail {
			summary["latest_version"] = app["latest_app_version"]
		}

		// Include portals (web URLs) if available
		if portals, ok := app["portals"].([]interface{}); ok && len(portals) > 0 {
			summary["portals"] = portals
		}

		// Include active workload summary
		if workloads, ok := app["active_workloads"].(map[string]interface{}); ok {
			if containers, ok := workloads["containers"].(float64); ok {
				summary["active_containers"] = int(containers)
			}
		}

		// Include config if requested
		if includeConfig {
			if config, ok := app["config"]; ok {
				summary["config"] = config
			}
		}

		// Include metadata
		if metadata, ok := app["metadata"].(map[string]interface{}); ok {
			summary["app_metadata"] = map[string]interface{}{
				"train":       metadata["train"],
				"description": metadata["description"],
			}
		}

		simplified = append(simplified, summary)
	}

	formatted, err := json.MarshalIndent(simplified, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func (r *Registry) handleUpgradeApp(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return "", fmt.Errorf("app_name is required")
	}

	version := "latest"
	if v, ok := args["version"].(string); ok && v != "" {
		version = v
	}

	snapshotHostpaths := true
	if s, ok := args["snapshot_hostpaths"].(bool); ok {
		snapshotHostpaths = s
	}

	// First, get upgrade summary to show what will be upgraded
	summaryResult, err := client.CallContext(ctx, "app.upgrade_summary", appName, map[string]interface{}{
		"app_version": version,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get upgrade summary: %w", err)
	}

	// Parse summary - can be either object or array depending on TrueNAS version/app
	var summary interface{}
	if err := json.Unmarshal(summaryResult, &summary); err != nil {
		return "", fmt.Errorf("failed to parse upgrade summary: %w", err)
	}

	// Perform the upgrade - this returns a job ID since it's a long-running operation
	upgradeOptions := map[string]interface{}{
		"app_version":        version,
		"snapshot_hostpaths": snapshotHostpaths,
	}

	result, err := client.CallContext(ctx, "app.upgrade", appName, upgradeOptions)
	if err != nil {
		return "", fmt.Errorf("failed to upgrade app: %w", err)
	}

	// Parse the job ID (app.upgrade may return an array [job_id] or just job_id)
	var jobID int
	// First try to parse as an integer
	if err := json.Unmarshal(result, &jobID); err != nil {
		// If that fails, try parsing as an array and extract the first element
		var jobIDArray []int
		if err2 := json.Unmarshal(result, &jobIDArray); err2 != nil {
			return "", fmt.Errorf("failed to parse job ID as int or array: int error: %v, array error: %v", err, err2)
		}
		if len(jobIDArray) == 0 {
			return "", fmt.Errorf("app.upgrade returned empty job ID array")
		}
		jobID = jobIDArray[0]
	}

	// Create task to track upgrade progress
	task, err := r.taskManager.CreateJobTask(
		"upgrade_app",
		args,
		jobID,
		1*time.Hour, // 1 hour TTL
	)
	if err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	response := map[string]interface{}{
		"app_name":         appName,
		"upgrade_summary":  summary,
		"task_id":          task.TaskID,
		"task_status":      task.Status,
		"poll_interval":    task.PollInterval,
		"job_id":           jobID,
		"snapshot_created": snapshotHostpaths,
		"message":          fmt.Sprintf("Upgrade started. Track progress with tasks_get using task_id: %s", task.TaskID),
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// handleUpgradeAppWithDryRun wraps the upgrade handler with dry-run support
func (r *Registry) handleUpgradeAppWithDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return ExecuteWithDryRun(ctx, client, args, &upgradeAppDryRun{}, r.handleUpgradeApp)
}

// upgradeAppDryRun implements dry-run preview for app upgrades
type upgradeAppDryRun struct{}

func (u *upgradeAppDryRun) ExecuteDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (*DryRunResult, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return nil, fmt.Errorf("app_name is required")
	}

	version := "latest"
	if v, ok := args["version"].(string); ok && v != "" {
		version = v
	}

	snapshotHostpaths := true
	if s, ok := args["snapshot_hostpaths"].(bool); ok {
		snapshotHostpaths = s
	}

	// Get current app state
	currentResult, err := client.CallContext(ctx, "app.query", []interface{}{
		[]interface{}{"name", "=", appName},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query app: %w", err)
	}

	var apps []map[string]interface{}
	if err := json.Unmarshal(currentResult, &apps); err != nil {
		return nil, fmt.Errorf("failed to parse app query: %w", err)
	}

	if len(apps) == 0 {
		return nil, fmt.Errorf("app %s not found", appName)
	}
	currentApp := apps[0]

	// Get upgrade summary
	summaryResult, err := client.CallContext(ctx, "app.upgrade_summary", appName, map[string]interface{}{
		"app_version": version,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get upgrade summary: %w", err)
	}

	// Parse summary - can be either object or array depending on TrueNAS version/app
	var summary interface{}
	if err := json.Unmarshal(summaryResult, &summary); err != nil {
		return nil, fmt.Errorf("failed to parse upgrade summary: %w", err)
	}

	// Build current state
	currentState := map[string]interface{}{
		"name":    currentApp["name"],
		"version": currentApp["human_version"],
		"state":   currentApp["state"],
	}

	// Build planned actions
	actions := []PlannedAction{
		{
			Step:        1,
			Description: "Stop application containers",
			Operation:   "stop",
			Target:      appName,
		},
		{
			Step:        2,
			Description: fmt.Sprintf("Upgrade from %v to %v", currentApp["human_version"], version),
			Operation:   "upgrade",
			Target:      appName,
			Details:     summary,
		},
		{
			Step:        3,
			Description: "Start application with new version",
			Operation:   "start",
			Target:      appName,
		},
	}

	result := &DryRunResult{
		Tool:           "upgrade_app",
		CurrentState:   currentState,
		PlannedActions: actions,
		EstimatedTime: &EstimatedTime{
			MinSeconds: 30,
			MaxSeconds: 300,
			Note:       "Time varies based on image size and network speed",
		},
	}

	// Add warnings if no snapshot
	if !snapshotHostpaths {
		result.Warnings = []string{
			"WARNING: snapshot_hostpaths is disabled. No backup will be created before upgrade.",
		}
	}

	return result, nil
}

func (r *Registry) handleStartApp(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return "", fmt.Errorf("app_name is required")
	}

	result, err := client.CallContext(ctx, "app.start", appName)
	if err != nil {
		return "", fmt.Errorf("failed to start app: %w", err)
	}

	var jobID int
	if err := json.Unmarshal(result, &jobID); err != nil {
		var jobIDArray []int
		if err2 := json.Unmarshal(result, &jobIDArray); err2 != nil {
			return "", fmt.Errorf("failed to parse job ID as int or array: int error: %v, array error: %v", err, err2)
		}
		if len(jobIDArray) == 0 {
			return "", fmt.Errorf("app.start returned empty job ID array")
		}
		jobID = jobIDArray[0]
	}

	task, err := r.taskManager.CreateJobTask("start_app", args, jobID, 10*time.Minute)
	if err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	response := map[string]interface{}{
		"app_name":      appName,
		"task_id":       task.TaskID,
		"task_status":   task.Status,
		"poll_interval": task.PollInterval,
		"job_id":        jobID,
		"message":       fmt.Sprintf("App start initiated. Track progress with tasks_get using task_id: %s", task.TaskID),
	}
	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format response: %w", err)
	}
	return string(formatted), nil
}

func (r *Registry) handleStartAppWithDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return ExecuteWithDryRun(ctx, client, args, &startAppDryRun{}, r.handleStartApp)
}

type startAppDryRun struct{}

func (s *startAppDryRun) ExecuteDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (*DryRunResult, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return nil, fmt.Errorf("app_name is required")
	}

	currentResult, err := client.CallContext(ctx, "app.query", []interface{}{
		[]interface{}{"name", "=", appName},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query app: %w", err)
	}

	var apps []map[string]interface{}
	if err := json.Unmarshal(currentResult, &apps); err != nil || len(apps) == 0 {
		return nil, fmt.Errorf("app not found: %s", appName)
	}

	currentState := apps[0]["state"]

	return &DryRunResult{
		Tool: "start_app",
		CurrentState: map[string]interface{}{
			"app_name": appName,
			"state":    currentState,
		},
		PlannedActions: []PlannedAction{
			{
				Step:        1,
				Description: "Start application containers",
				Operation:   "start",
				Target:      "app.start",
				Details:     map[string]interface{}{"app_name": appName},
			},
		},
		Warnings: []string{
			fmt.Sprintf("App is currently in state: %v. App must be STOPPED to start.", currentState),
		},
		EstimatedTime: &EstimatedTime{MinSeconds: 5, MaxSeconds: 120, Note: "Depends on app startup time"},
	}, nil
}

func (r *Registry) handleStopApp(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return "", fmt.Errorf("app_name is required")
	}

	result, err := client.CallContext(ctx, "app.stop", appName)
	if err != nil {
		return "", fmt.Errorf("failed to stop app: %w", err)
	}

	var jobID int
	if err := json.Unmarshal(result, &jobID); err != nil {
		var jobIDArray []int
		if err2 := json.Unmarshal(result, &jobIDArray); err2 != nil {
			return "", fmt.Errorf("failed to parse job ID as int or array: int error: %v, array error: %v", err, err2)
		}
		if len(jobIDArray) == 0 {
			return "", fmt.Errorf("app.stop returned empty job ID array")
		}
		jobID = jobIDArray[0]
	}

	task, err := r.taskManager.CreateJobTask("stop_app", args, jobID, 5*time.Minute)
	if err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	response := map[string]interface{}{
		"app_name":      appName,
		"task_id":       task.TaskID,
		"task_status":   task.Status,
		"poll_interval": task.PollInterval,
		"job_id":        jobID,
		"message":       fmt.Sprintf("App stop initiated. Track progress with tasks_get using task_id: %s", task.TaskID),
	}
	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format response: %w", err)
	}
	return string(formatted), nil
}

func (r *Registry) handleStopAppWithDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return ExecuteWithDryRun(ctx, client, args, &stopAppDryRun{}, r.handleStopApp)
}

type stopAppDryRun struct{}

func (s *stopAppDryRun) ExecuteDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (*DryRunResult, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return nil, fmt.Errorf("app_name is required")
	}

	currentResult, err := client.CallContext(ctx, "app.query", []interface{}{
		[]interface{}{"name", "=", appName},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query app: %w", err)
	}

	var apps []map[string]interface{}
	if err := json.Unmarshal(currentResult, &apps); err != nil || len(apps) == 0 {
		return nil, fmt.Errorf("app not found: %s", appName)
	}

	currentState := apps[0]["state"]

	return &DryRunResult{
		Tool: "stop_app",
		CurrentState: map[string]interface{}{
			"app_name": appName,
			"state":    currentState,
		},
		PlannedActions: []PlannedAction{
			{
				Step:        1,
				Description: "Stop application containers",
				Operation:   "stop",
				Target:      "app.stop",
				Details:     map[string]interface{}{"app_name": appName},
			},
		},
		Warnings: []string{
			fmt.Sprintf("App '%s' (currently %v) will become unavailable after stopping.", appName, currentState),
		},
		EstimatedTime: &EstimatedTime{MinSeconds: 5, MaxSeconds: 60, Note: "Depends on app shutdown time"},
	}, nil
}

// handleGetAppConfig retrieves the user-specified config for an app
func handleGetAppConfig(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return "", fmt.Errorf("app_name is required")
	}

	result, err := client.CallContext(ctx, "app.config", appName)
	if err != nil {
		return "", fmt.Errorf("failed to get app config: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(result, &config); err != nil {
		return "", fmt.Errorf("failed to parse app config: %w", err)
	}

	response := map[string]interface{}{
		"app_name": appName,
		"config":   config,
	}
	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}
	return string(formatted), nil
}

// handleUpdateApp performs the actual app.update API call
func (r *Registry) handleUpdateApp(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return "", fmt.Errorf("app_name is required")
	}

	values, ok := args["values"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("values is required and must be an object")
	}

	if err := enforceHostPathStorage(values); err != nil {
		return "", fmt.Errorf("storage validation failed: %v", err)
	}

	result, err := client.CallContext(ctx, "app.update", appName, map[string]interface{}{
		"values": values,
	})
	if err != nil {
		return "", fmt.Errorf("failed to update app: %w", err)
	}

	var jobID int
	if err := json.Unmarshal(result, &jobID); err != nil {
		var jobIDArray []int
		if err2 := json.Unmarshal(result, &jobIDArray); err2 != nil {
			return "", fmt.Errorf("failed to parse job ID: int error: %v, array error: %v", err, err2)
		}
		if len(jobIDArray) == 0 {
			return "", fmt.Errorf("app.update returned empty job ID array")
		}
		jobID = jobIDArray[0]
	}

	task, err := r.taskManager.CreateJobTask("update_app", args, jobID, 30*time.Minute)
	if err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	response := map[string]interface{}{
		"app_name":      appName,
		"task_id":       task.TaskID,
		"task_status":   task.Status,
		"poll_interval": task.PollInterval,
		"job_id":        jobID,
		"message":       fmt.Sprintf("App update initiated. Track progress with tasks_get using task_id: %s", task.TaskID),
	}
	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to format response: %w", err)
	}
	return string(formatted), nil
}

func (r *Registry) handleUpdateAppWithDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return ExecuteWithDryRun(ctx, client, args, &updateAppDryRun{}, r.handleUpdateApp)
}

type updateAppDryRun struct{}

func (u *updateAppDryRun) ExecuteDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (*DryRunResult, error) {
	appName, ok := args["app_name"].(string)
	if !ok || appName == "" {
		return nil, fmt.Errorf("app_name is required")
	}

	values, ok := args["values"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("values is required and must be an object")
	}

	if err := enforceHostPathStorage(values); err != nil {
		return nil, fmt.Errorf("storage validation failed: %v", err)
	}

	currentResult, err := client.CallContext(ctx, "app.config", appName)
	if err != nil {
		return nil, fmt.Errorf("failed to get current config: %w", err)
	}
	var currentConfig map[string]interface{}
	json.Unmarshal(currentResult, &currentConfig)

	return &DryRunResult{
		Tool: "update_app",
		CurrentState: map[string]interface{}{
			"app_name":       appName,
			"current_config": currentConfig,
		},
		PlannedActions: []PlannedAction{
			{
				Step:        1,
				Description: "Apply new configuration values to app",
				Operation:   "update",
				Target:      "app.update",
				Details: map[string]interface{}{
					"app_name":   appName,
					"new_values": values,
				},
			},
		},
		Warnings: []string{
			"App containers will be restarted to apply configuration changes.",
		},
		EstimatedTime: &EstimatedTime{MinSeconds: 10, MaxSeconds: 300, Note: "Depends on app restart time"},
	}, nil
}
