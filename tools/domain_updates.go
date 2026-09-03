package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Updates tools: TrueNAS update download, apply and status.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerUpdatesTools() {
	// System update tools
	r.tools["check_updates"] = Tool{
		Definition: mcp.Tool{
			Name:        "check_updates",
			Description: "Check for available TrueNAS system updates",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleCheckUpdates,
	}

	r.tools["download_update"] = Tool{
		Definition: mcp.Tool{
			Name:        "download_update",
			Description: "Download TrueNAS system update. Supports dry-run mode to preview changes. Returns a task ID for tracking download progress. This is a write operation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview changes without executing (default: false)",
						"default":     false,
					},
				},
			},
		},
		Handler: r.handleDownloadUpdateWithDryRun,
	}

	r.tools["apply_update"] = Tool{
		Definition: mcp.Tool{
			Name:        "apply_update",
			Description: "Apply downloaded TrueNAS system update. System will reboot if reboot parameter is true. Supports dry-run mode to preview changes. Returns a task ID for tracking progress. This is a write operation. **Best Practice**: After successful update and reboot, use query_boot_environments to check for old boot environments that can be safely pruned with delete_boot_environment. Recommend keeping 2-3 recent boot environments for rollback safety.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"reboot": map[string]interface{}{
						"type":        "boolean",
						"description": "Reboot after update completes (default: false for safety)",
						"default":     false,
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview changes without executing (default: false)",
						"default":     false,
					},
				},
			},
		},
		Handler: r.handleApplyUpdateWithDryRun,
	}

	r.tools["update_status"] = Tool{
		Definition: mcp.Tool{
			Name:        "update_status",
			Description: "Get current TrueNAS system update status and progress",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleUpdateStatus,
	}
}

// handleCheckUpdates checks for available TrueNAS system updates
func handleCheckUpdates(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	result, err := client.CallContext(ctx, "update.available_versions")
	if err != nil {
		return "", fmt.Errorf("failed to check for updates: %w", err)
	}

	var updates interface{}
	if err := json.Unmarshal(result, &updates); err != nil {
		return "", fmt.Errorf("failed to parse update information: %w", err)
	}

	formatted, err := json.MarshalIndent(updates, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// handleUpdateStatus gets current system update status
func handleUpdateStatus(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	result, err := client.CallContext(ctx, "update.status")
	if err != nil {
		return "", fmt.Errorf("failed to get update status: %w", err)
	}

	var status interface{}
	if err := json.Unmarshal(result, &status); err != nil {
		return "", fmt.Errorf("failed to parse update status: %w", err)
	}

	formatted, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// handleDownloadUpdate downloads a TrueNAS system update
func (r *Registry) handleDownloadUpdate(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	train, _ := args["train"].(string)
	version, _ := args["version"].(string)

	// Check if update is already downloaded
	statusResult, err := client.CallContext(ctx, "update.status")
	if err == nil {
		var status map[string]interface{}
		if err := json.Unmarshal(statusResult, &status); err == nil {
			// Check if download is complete
			if progress, ok := status["update_download_progress"].(map[string]interface{}); ok {
				if percent, ok := progress["percent"].(float64); ok && percent == 100 {
					if dlVersion, ok := progress["version"].(string); ok {
						// If no specific version requested, or versions match
						if version == "" || dlVersion == version {
							response := map[string]interface{}{
								"train":              train,
								"version":            dlVersion,
								"already_downloaded": true,
								"download_percent":   100,
								"message":            fmt.Sprintf("Update %s is already downloaded (100%%). Ready to apply.", dlVersion),
							}
							formatted, _ := json.MarshalIndent(response, "", "  ")
							return string(formatted), nil
						}
					}
				}
			}
		}
	}

	// Start the download (update.download typically takes no parameters)
	// TrueNAS downloads based on the configured train automatically
	result, err := client.CallContext(ctx, "update.download")
	if err != nil {
		return "", fmt.Errorf("failed to start update download: %w", err)
	}

	// Parse job ID
	var jobID int
	if err := json.Unmarshal(result, &jobID); err != nil {
		return "", fmt.Errorf("failed to parse job ID: %w", err)
	}

	// Create task to track download progress
	task, err := r.taskManager.CreateJobTask(
		"download_update",
		args,
		jobID,
		2*time.Hour, // 2 hour TTL
	)
	if err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	response := map[string]interface{}{
		"train":         train,
		"version":       version,
		"task_id":       task.TaskID,
		"task_status":   task.Status,
		"poll_interval": task.PollInterval,
		"job_id":        jobID,
		"message":       fmt.Sprintf("Update download started. Track progress with tasks_get using task_id: %s", task.TaskID),
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// handleDownloadUpdateWithDryRun wraps the download handler with dry-run support
func (r *Registry) handleDownloadUpdateWithDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return ExecuteWithDryRun(ctx, client, args, &downloadUpdateDryRun{}, r.handleDownloadUpdate)
}

// downloadUpdateDryRun implements dry-run preview for update downloads
type downloadUpdateDryRun struct{}

func (d *downloadUpdateDryRun) ExecuteDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (*DryRunResult, error) {
	train, _ := args["train"].(string)
	version, _ := args["version"].(string)

	// Get current system info
	sysInfoResult, err := client.CallContext(ctx, "system.info")
	if err != nil {
		return nil, fmt.Errorf("failed to get system info: %w", err)
	}

	var sysInfo map[string]interface{}
	if err := json.Unmarshal(sysInfoResult, &sysInfo); err != nil {
		return nil, fmt.Errorf("failed to parse system info: %w", err)
	}

	currentVersion := sysInfo["version"].(string)

	actions := []PlannedAction{
		{
			Step:        1,
			Description: "Connect to TrueNAS update server",
			Operation:   "connect",
			Target:      "update.truenas.com",
		},
		{
			Step:        2,
			Description: fmt.Sprintf("Download update files for version %s", version),
			Operation:   "download",
			Target:      version,
			Details: map[string]interface{}{
				"train":   train,
				"version": version,
			},
		},
		{
			Step:        3,
			Description: "Verify update package integrity",
			Operation:   "verify",
			Target:      version,
		},
	}

	result := &DryRunResult{
		Tool: "download_update",
		CurrentState: map[string]interface{}{
			"current_version": currentVersion,
		},
		PlannedActions: actions,
		EstimatedTime: &EstimatedTime{
			MinSeconds: 120,
			MaxSeconds: 1800,
			Note:       "Time varies based on update size and network speed",
		},
	}

	return result, nil
}

// handleApplyUpdate applies a downloaded TrueNAS system update
func (r *Registry) handleApplyUpdate(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	reboot := false
	if r, ok := args["reboot"].(bool); ok {
		reboot = r
	}

	// Build update options
	updateOptions := map[string]interface{}{
		"reboot": reboot,
	}

	// Start the update
	result, err := client.CallContext(ctx, "update.run", updateOptions)
	if err != nil {
		return "", fmt.Errorf("failed to start update: %w", err)
	}

	// update.run returns a job ID
	var jobID int
	if err := json.Unmarshal(result, &jobID); err != nil {
		return "", fmt.Errorf("failed to parse job ID: %w", err)
	}

	// Create job-based task to track update progress
	task, err := r.taskManager.CreateJobTask(
		"apply_update",
		args,
		jobID,
		2*time.Hour, // 2 hour TTL
	)
	if err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	response := map[string]interface{}{
		"reboot":        reboot,
		"task_id":       task.TaskID,
		"task_status":   task.Status,
		"poll_interval": task.PollInterval,
		"job_id":        jobID,
		"message":       fmt.Sprintf("Update started. Track progress with tasks_get using task_id: %s", task.TaskID),
	}

	if reboot {
		response["warning"] = "System will reboot after update completes. Connection will be lost."
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// handleApplyUpdateWithDryRun wraps the apply handler with dry-run support
func (r *Registry) handleApplyUpdateWithDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return ExecuteWithDryRun(ctx, client, args, &applyUpdateDryRun{}, r.handleApplyUpdate)
}

// applyUpdateDryRun implements dry-run preview for update application
type applyUpdateDryRun struct{}

func (a *applyUpdateDryRun) ExecuteDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (*DryRunResult, error) {
	reboot := false
	if r, ok := args["reboot"].(bool); ok {
		reboot = r
	}

	// Get current system info
	sysInfoResult, err := client.CallContext(ctx, "system.info")
	if err != nil {
		return nil, fmt.Errorf("failed to get system info: %w", err)
	}

	var sysInfo map[string]interface{}
	if err := json.Unmarshal(sysInfoResult, &sysInfo); err != nil {
		return nil, fmt.Errorf("failed to parse system info: %w", err)
	}

	currentVersion := sysInfo["version"].(string)

	// Check update status to get target version
	statusResult, err := client.CallContext(ctx, "update.status")
	if err != nil {
		return nil, fmt.Errorf("failed to get update status: %w", err)
	}

	var status map[string]interface{}
	if err := json.Unmarshal(statusResult, &status); err != nil {
		return nil, fmt.Errorf("failed to parse update status: %w", err)
	}

	actions := []PlannedAction{
		{
			Step:        1,
			Description: "Stop critical system services",
			Operation:   "stop",
			Target:      "system services",
		},
		{
			Step:        2,
			Description: "Apply system update",
			Operation:   "update",
			Target:      "system",
			Details:     status,
		},
		{
			Step:        3,
			Description: "Verify update installation",
			Operation:   "verify",
			Target:      "system",
		},
	}

	if reboot {
		actions = append(actions, PlannedAction{
			Step:        4,
			Description: "Reboot system to complete update",
			Operation:   "reboot",
			Target:      "system",
		})
	}

	result := &DryRunResult{
		Tool: "apply_update",
		CurrentState: map[string]interface{}{
			"current_version": currentVersion,
			"update_status":   status,
		},
		PlannedActions: actions,
		EstimatedTime: &EstimatedTime{
			MinSeconds: 180,
			MaxSeconds: 900,
			Note:       "Time varies based on system configuration. Add 60-120s for reboot if enabled.",
		},
		Warnings: []string{
			"CRITICAL: This operation will update the TrueNAS system software.",
			"Services may be interrupted during the update process.",
		},
	}

	if reboot {
		result.Warnings = append(result.Warnings,
			"REBOOT ENABLED: System will automatically reboot after update completes.",
			"All connections will be lost during reboot.",
		)
	} else {
		result.Warnings = append(result.Warnings,
			"Manual reboot required after update to complete the process.",
		)
	}

	return result, nil
}
