package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// System tools: system information, health and reboot.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerSystemTools() {
	// System info tool
	r.tools["system_info"] = Tool{
		Definition: mcp.Tool{
			Name:        "system_info",
			Description: "Get TrueNAS system information including version, hostname, and platform details",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleSystemInfo,
	}

	// System health tool
	r.tools["system_health"] = Tool{
		Definition: mcp.Tool{
			Name:        "system_health",
			Description: "Get system health status including alerts and diagnostics",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleSystemHealth,
	}

	// System reboot tool
	r.tools["system_reboot"] = Tool{
		Definition: mcp.Tool{
			Name:        "system_reboot",
			Description: "Reboot the TrueNAS system. This will disconnect all active sessions and services. Use after applying system updates.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleSystemReboot,
	}
}

// Tool handlers

func handleSystemInfo(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	result, err := client.CallContext(ctx, "system.info")
	if err != nil {
		return "", err
	}

	var info map[string]interface{}
	if err := json.Unmarshal(result, &info); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	formatted, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleSystemHealth(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	// Get alerts
	result, err := client.CallContext(ctx, "alert.list")
	if err != nil {
		return "", err
	}

	var alerts []map[string]interface{}
	if err := json.Unmarshal(result, &alerts); err != nil {
		return "", fmt.Errorf("failed to parse alerts: %w", err)
	}

	// Get active jobs
	jobsResult, err := client.CallContext(ctx, "core.get_jobs", []interface{}{
		[]interface{}{"state", "=", "RUNNING"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get jobs: %w", err)
	}

	var jobs []map[string]interface{}
	if err := json.Unmarshal(jobsResult, &jobs); err != nil {
		return "", fmt.Errorf("failed to parse jobs: %w", err)
	}

	// Create summary of active jobs
	activeTasks := make([]map[string]interface{}, 0)
	for _, job := range jobs {
		taskSummary := map[string]interface{}{
			"id":          job["id"],
			"method":      job["method"],
			"state":       job["state"],
			"description": job["description"],
		}
		if progress, ok := job["progress"]; ok {
			taskSummary["progress"] = progress
		}
		activeTasks = append(activeTasks, taskSummary)
	}

	// Add capacity warnings
	capacityWarnings := make([]string, 0)

	// Quick capacity check using reporting data (last hour)
	cpuResult, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
		map[string]interface{}{
			"name":       "cpu",
			"identifier": nil,
		},
	}, map[string]interface{}{"unit": "HOUR"})
	if err == nil {
		var cpuData []map[string]interface{}
		if err := json.Unmarshal(cpuResult, &cpuData); err == nil && len(cpuData) > 0 {
			if dataPoints, err := extractDataPoints(cpuData[0]); err == nil {
				avgCPU := calculateAverage(dataPoints)
				if avgCPU > 85 {
					capacityWarnings = append(capacityWarnings,
						fmt.Sprintf("CPU utilization critical: %.1f%%", avgCPU))
				} else if avgCPU > 70 {
					capacityWarnings = append(capacityWarnings,
						fmt.Sprintf("CPU utilization elevated: %.1f%%", avgCPU))
				}
			}
		}
	}

	// Check memory
	sysInfoResult, err := client.CallContext(ctx, "system.info")
	var totalMemory float64
	if err == nil {
		var sysInfo map[string]interface{}
		if err := json.Unmarshal(sysInfoResult, &sysInfo); err == nil {
			if physMem, ok := sysInfo["physmem"].(float64); ok {
				totalMemory = physMem
			}
		}
	}

	if totalMemory > 0 {
		memResult, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
			map[string]interface{}{
				"name":       "memory",
				"identifier": nil,
			},
		}, map[string]interface{}{"unit": "HOUR"})
		if err == nil {
			var memData []map[string]interface{}
			if err := json.Unmarshal(memResult, &memData); err == nil && len(memData) > 0 {
				if dataPoints, err := extractDataPoints(memData[0]); err == nil {
					// Convert to percentage
					avgMemBytes := calculateAverage(dataPoints)
					avgMemPct := (avgMemBytes / totalMemory) * 100
					if avgMemPct > 85 {
						capacityWarnings = append(capacityWarnings,
							fmt.Sprintf("Memory utilization critical: %.1f%%", avgMemPct))
					} else if avgMemPct > 70 {
						capacityWarnings = append(capacityWarnings,
							fmt.Sprintf("Memory utilization elevated: %.1f%%", avgMemPct))
					}
				}
			}
		}
	}

	// Check pool capacity
	poolResult, err := client.CallContext(ctx, "pool.query")
	if err == nil {
		var pools []map[string]interface{}
		if err := json.Unmarshal(poolResult, &pools); err == nil {
			for _, pool := range pools {
				poolName, _ := pool["name"].(string)
				capacity := calculatePoolCapacity(pool)

				if utilPct, ok := capacity["utilization_pct"].(float64); ok {
					if utilPct > 85 {
						capacityWarnings = append(capacityWarnings,
							fmt.Sprintf("Pool '%s' capacity critical: %.1f%%", poolName, utilPct))
					} else if utilPct > 70 {
						capacityWarnings = append(capacityWarnings,
							fmt.Sprintf("Pool '%s' capacity elevated: %.1f%%", poolName, utilPct))
					}
				}
			}
		}
	}

	// Check directory service status
	var directoryServiceStatus map[string]interface{}
	dirStatusResult, err := client.CallContext(ctx, "directoryservices.status")
	if err == nil {
		var dirStatus map[string]interface{}
		if err := json.Unmarshal(dirStatusResult, &dirStatus); err == nil {
			directoryServiceStatus = dirStatus

			// Add warnings for directory service issues
			if status, ok := dirStatus["status"].(string); ok && status != "" {
				if status == "FAULTED" {
					statusMsg := "connection error"
					if msg, ok := dirStatus["status_msg"].(string); ok && msg != "" {
						statusMsg = msg
					}
					serviceType := "directory service"
					if svcType, ok := dirStatus["type"].(string); ok && svcType != "" {
						serviceType = svcType
					}
					capacityWarnings = append(capacityWarnings,
						fmt.Sprintf("CRITICAL: Directory service (%s) is FAULTED: %s", serviceType, statusMsg))
				} else if status == "JOINING" || status == "LEAVING" {
					capacityWarnings = append(capacityWarnings,
						fmt.Sprintf("Directory service operation in progress: %s", status))
				}
			}
		}
	}

	response := map[string]interface{}{
		"alerts":            alerts,
		"alert_count":       len(alerts),
		"active_jobs":       activeTasks,
		"job_count":         len(activeTasks),
		"capacity_warnings": capacityWarnings,
		"directory_service": directoryServiceStatus,
		"health_check":      "OK",
	}

	if len(alerts) > 0 {
		response["health_check"] = "ALERTS_PRESENT"
	}

	if len(activeTasks) > 0 {
		if response["health_check"] == "OK" {
			response["health_check"] = "ACTIVE_TASKS"
		} else {
			response["health_check"] = "ALERTS_AND_ACTIVE_TASKS"
		}
	}

	if len(capacityWarnings) > 0 {
		if response["health_check"] == "OK" {
			response["health_check"] = "CAPACITY_WARNINGS"
		} else {
			response["health_check"] = response["health_check"].(string) + "_AND_CAPACITY"
		}
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// handleSystemReboot reboots the TrueNAS system
func handleSystemReboot(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	// Call system.reboot with reason parameter
	reason := "System reboot requested via MCP"
	result, err := client.CallContext(ctx, "system.reboot", reason)
	if err != nil {
		return "", fmt.Errorf("failed to initiate system reboot: %w", err)
	}

	// system.reboot typically returns nothing or a simple acknowledgment
	var response map[string]interface{}
	if len(result) > 0 {
		_ = json.Unmarshal(result, &response)
	}

	returnMsg := map[string]interface{}{
		"status":  "reboot_initiated",
		"message": "System reboot initiated. All connections will be lost.",
		"warning": "TrueNAS system is rebooting. Wait approximately 2-3 minutes before reconnecting.",
	}

	formatted, err := json.MarshalIndent(returnMsg, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}
