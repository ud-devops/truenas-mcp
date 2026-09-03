package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Alerts tools: alert listing, dismissal and restore.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerAlertsTools() {
	// Alert list with filtering
	r.tools["list_alerts"] = Tool{
		Definition: mcp.Tool{
			Name:        "list_alerts",
			Description: "List system alerts with optional filtering by dismissed status",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dismissed": map[string]interface{}{
						"type":        "boolean",
						"description": "Filter by dismissed status (true=dismissed only, false=active only, omit=all)",
					},
				},
			},
		},
		Handler: handleListAlerts,
	}

	// Dismiss alert
	r.tools["dismiss_alert"] = Tool{
		Definition: mcp.Tool{
			Name:        "dismiss_alert",
			Description: "Dismiss a system alert by UUID",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"uuid": map[string]interface{}{
						"type":        "string",
						"description": "UUID of the alert to dismiss",
					},
				},
				"required": []string{"uuid"},
			},
		},
		Handler: handleDismissAlert,
	}

	// Restore alert
	r.tools["restore_alert"] = Tool{
		Definition: mcp.Tool{
			Name:        "restore_alert",
			Description: "Restore (un-dismiss) a previously dismissed alert by UUID",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"uuid": map[string]interface{}{
						"type":        "string",
						"description": "UUID of the alert to restore",
					},
				},
				"required": []string{"uuid"},
			},
		},
		Handler: handleRestoreAlert,
	}
}

// Alert management handlers

func handleListAlerts(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	// alert.list doesn't take filter parameters in the same way as other queries
	// It just returns all alerts, so we'll filter in post-processing if needed
	result, err := client.CallContext(ctx, "alert.list")
	if err != nil {
		return "", err
	}

	var alerts []map[string]interface{}
	if err := json.Unmarshal(result, &alerts); err != nil {
		return "", fmt.Errorf("failed to parse alerts: %w", err)
	}

	// Post-filter by dismissed status if requested
	if dismissed, ok := args["dismissed"].(bool); ok {
		filtered := make([]map[string]interface{}, 0)
		for _, alert := range alerts {
			if isDismissed, ok := alert["dismissed"].(bool); ok && isDismissed == dismissed {
				filtered = append(filtered, alert)
			}
		}
		alerts = filtered
	}

	formatted, err := json.MarshalIndent(alerts, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleDismissAlert(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	uuid, ok := args["uuid"].(string)
	if !ok || uuid == "" {
		return "", fmt.Errorf("uuid parameter is required")
	}

	result, err := client.CallContext(ctx, "alert.dismiss", uuid)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Alert %s dismissed successfully: %s", uuid, string(result)), nil
}

func handleRestoreAlert(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	uuid, ok := args["uuid"].(string)
	if !ok || uuid == "" {
		return "", fmt.Errorf("uuid parameter is required")
	}

	result, err := client.CallContext(ctx, "alert.restore", uuid)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Alert %s restored successfully: %s", uuid, string(result)), nil
}
