package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Capacity tools: capacity analysis and projection.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerCapacityTools() {
	// Capacity analysis tool
	r.tools["analyze_capacity"] = Tool{
		Definition: mcp.Tool{
			Name:        "analyze_capacity",
			Description: "Analyze system capacity utilization and trends for capacity planning. Provides utilization percentages, growth rates, and projections based on historical metrics. Includes CPU, memory, network, and disk I/O analysis.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"time_range": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"DAY", "WEEK", "MONTH", "YEAR"},
						"description": "Historical time range for trend analysis (default: MONTH for ~90 days)",
						"default":     "MONTH",
					},
					"metrics": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
							"enum": []string{"cpu", "memory", "network", "disk", "all"},
						},
						"description": "Metrics to analyze (default: all)",
					},
				},
			},
		},
		Handler: handleAnalyzeCapacity,
	}

	// Pool capacity details tool
	r.tools["get_pool_capacity_details"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_pool_capacity_details",
			Description: "Get detailed pool and dataset capacity information with utilization analysis. Returns current capacity snapshot with breakdown by dataset. Note: Historical capacity trends are not available from TrueNAS API; use Netdata graphs if available.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pool_name": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Specific pool to analyze",
					},
				},
			},
		},
		Handler: handleGetPoolCapacityDetails,
	}
}

// Capacity analysis handlers

func handleAnalyzeCapacity(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	timeRange := "MONTH"
	if tr, ok := args["time_range"].(string); ok && tr != "" {
		timeRange = tr
	}

	// Default to all metrics
	metrics := []string{"cpu", "memory", "network", "disk"}
	if m, ok := args["metrics"].([]interface{}); ok && len(m) > 0 {
		metrics = make([]string, 0, len(m))
		for _, v := range m {
			if s, ok := v.(string); ok {
				if s == "all" {
					metrics = []string{"cpu", "memory", "network", "disk"}
					break
				}
				metrics = append(metrics, s)
			}
		}
	}

	analysis := make(map[string]interface{})

	// Analyze each metric
	for _, metric := range metrics {
		switch metric {
		case "cpu":
			cpuAnalysis, err := analyzeCPUCapacity(ctx, client, timeRange)
			if err != nil {
				analysis["cpu"] = map[string]string{"error": err.Error()}
			} else {
				analysis["cpu"] = cpuAnalysis
			}
		case "memory":
			memAnalysis, err := analyzeMemoryCapacity(ctx, client, timeRange)
			if err != nil {
				analysis["memory"] = map[string]string{"error": err.Error()}
			} else {
				analysis["memory"] = memAnalysis
			}
		case "network":
			netAnalysis, err := analyzeNetworkCapacity(ctx, client, timeRange)
			if err != nil {
				analysis["network"] = map[string]string{"error": err.Error()}
			} else {
				analysis["network"] = netAnalysis
			}
		case "disk":
			diskAnalysis, err := analyzeDiskCapacity(ctx, client, timeRange)
			if err != nil {
				analysis["disk"] = map[string]string{"error": err.Error()}
			} else {
				analysis["disk"] = diskAnalysis
			}
		}
	}

	// Add summary and recommendations
	analysis["summary"] = generateCapacityRecommendations(analysis)

	formatted, err := json.MarshalIndent(analysis, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func analyzeCPUCapacity(ctx context.Context, client *truenas.Client, timeRange string) (map[string]interface{}, error) {
	// Get CPU metrics for time range
	result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
		map[string]interface{}{
			"name":       "cpu",
			"identifier": nil,
		},
	}, map[string]interface{}{"unit": timeRange})

	if err != nil {
		return nil, err
	}

	var metricsData []map[string]interface{}
	if err := json.Unmarshal(result, &metricsData); err != nil {
		return nil, err
	}

	if len(metricsData) == 0 {
		return nil, fmt.Errorf("no CPU metrics data available")
	}

	// Extract data points from the first metric (CPU usage)
	dataPoints, err := extractDataPoints(metricsData[0])
	if err != nil {
		return nil, err
	}

	// Calculate statistics
	current := calculateRecentAverage(dataPoints, 5) // Last 5 points
	average := calculateAverage(dataPoints)
	peak := calculateMax(dataPoints)
	trend := calculateTrendDirection(dataPoints)
	status := determineCapacityStatus(current, 70.0, 85.0)

	analysis := map[string]interface{}{
		"metric":                  "CPU",
		"time_range":              timeRange,
		"current_utilization_pct": fmt.Sprintf("%.2f", current),
		"average_utilization_pct": fmt.Sprintf("%.2f", average),
		"peak_utilization_pct":    fmt.Sprintf("%.2f", peak),
		"trend":                   trend,
		"capacity_status":         status,
	}

	// Add projections if trending up
	if trend == "increasing" {
		projections := calculateProjections(dataPoints, current, 70.0, 85.0)
		if len(projections) > 0 {
			analysis["projections"] = projections
		}
	}

	return analysis, nil
}

func analyzeMemoryCapacity(ctx context.Context, client *truenas.Client, timeRange string) (map[string]interface{}, error) {
	// Get system info to find total memory
	sysInfoResult, err := client.CallContext(ctx, "system.info")
	if err != nil {
		return nil, fmt.Errorf("failed to get system info: %w", err)
	}

	var sysInfo map[string]interface{}
	if err := json.Unmarshal(sysInfoResult, &sysInfo); err != nil {
		return nil, fmt.Errorf("failed to parse system info: %w", err)
	}

	// Get total physical memory in bytes
	totalMemory := 0.0
	if physMem, ok := sysInfo["physmem"].(float64); ok {
		totalMemory = physMem
	} else {
		return nil, fmt.Errorf("could not determine total system memory")
	}

	// Get memory metrics
	result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
		map[string]interface{}{
			"name":       "memory",
			"identifier": nil,
		},
	}, map[string]interface{}{"unit": timeRange})

	if err != nil {
		return nil, err
	}

	var metricsData []map[string]interface{}
	if err := json.Unmarshal(result, &metricsData); err != nil {
		return nil, err
	}

	if len(metricsData) == 0 {
		return nil, fmt.Errorf("no memory metrics data available")
	}

	// Extract data points (in bytes)
	dataPoints, err := extractDataPoints(metricsData[0])
	if err != nil {
		return nil, err
	}

	// Convert to percentages
	dataPointsPct := make([]float64, len(dataPoints))
	for i, dp := range dataPoints {
		dataPointsPct[i] = (dp / totalMemory) * 100
	}

	// Calculate statistics
	current := calculateRecentAverage(dataPointsPct, 5)
	average := calculateAverage(dataPointsPct)
	peak := calculateMax(dataPointsPct)
	trend := calculateTrendDirection(dataPointsPct)
	status := determineCapacityStatus(current, 70.0, 85.0)

	analysis := map[string]interface{}{
		"metric":                  "Memory",
		"time_range":              timeRange,
		"current_utilization_pct": fmt.Sprintf("%.2f", current),
		"average_utilization_pct": fmt.Sprintf("%.2f", average),
		"peak_utilization_pct":    fmt.Sprintf("%.2f", peak),
		"trend":                   trend,
		"capacity_status":         status,
		"total_memory_bytes":      int64(totalMemory),
	}

	// Add projections if trending up
	if trend == "increasing" {
		projections := calculateProjections(dataPointsPct, current, 70.0, 85.0)
		if len(projections) > 0 {
			analysis["projections"] = projections
		}
	}

	return analysis, nil
}

func analyzeNetworkCapacity(ctx context.Context, client *truenas.Client, timeRange string) (map[string]interface{}, error) {
	// Get all network interfaces
	ifaceResult, err := client.CallContext(ctx, "interface.query")
	if err != nil {
		return nil, fmt.Errorf("failed to query interfaces: %w", err)
	}

	var ifaceList []map[string]interface{}
	if err := json.Unmarshal(ifaceResult, &ifaceList); err != nil {
		return nil, fmt.Errorf("failed to parse interface list: %w", err)
	}

	interfaceAnalysis := make(map[string]interface{})

	for _, iface := range ifaceList {
		ifaceName, ok := iface["name"].(string)
		if !ok || ifaceName == "" {
			continue
		}

		// Get link speed if available
		var linkSpeed float64
		if state, ok := iface["state"].(map[string]interface{}); ok {
			if speed, ok := state["link_speed"].(float64); ok {
				linkSpeed = speed // In Mbps
			}
		}

		// Get network metrics for this interface
		result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
			map[string]interface{}{
				"name":       "interface",
				"identifier": ifaceName,
			},
		}, map[string]interface{}{"unit": timeRange})

		if err != nil {
			interfaceAnalysis[ifaceName] = map[string]string{"error": err.Error()}
			continue
		}

		var metricsData []map[string]interface{}
		if err := json.Unmarshal(result, &metricsData); err != nil {
			interfaceAnalysis[ifaceName] = map[string]string{"error": fmt.Sprintf("parse error: %v", err)}
			continue
		}

		if len(metricsData) == 0 {
			continue
		}

		// Analyze both TX and RX
		ifaceInfo := make(map[string]interface{})
		if linkSpeed > 0 {
			ifaceInfo["link_speed_mbps"] = linkSpeed
		}

		for _, metric := range metricsData {
			legend, _ := metric["legend"].(string)
			dataPoints, err := extractDataPoints(metric)
			if err != nil {
				continue
			}

			// Convert bits/s to Mbps for comparison with link speed
			dataPointsMbps := make([]float64, len(dataPoints))
			for i, dp := range dataPoints {
				dataPointsMbps[i] = dp / 1000000.0
			}

			current := calculateRecentAverage(dataPointsMbps, 5)
			average := calculateAverage(dataPointsMbps)
			peak := calculateMax(dataPointsMbps)

			metricInfo := map[string]interface{}{
				"current_mbps": fmt.Sprintf("%.2f", current),
				"average_mbps": fmt.Sprintf("%.2f", average),
				"peak_mbps":    fmt.Sprintf("%.2f", peak),
			}

			// Calculate utilization percentage if we have link speed
			if linkSpeed > 0 {
				currentPct := (current / linkSpeed) * 100
				avgPct := (average / linkSpeed) * 100
				peakPct := (peak / linkSpeed) * 100

				metricInfo["current_utilization_pct"] = fmt.Sprintf("%.2f", currentPct)
				metricInfo["average_utilization_pct"] = fmt.Sprintf("%.2f", avgPct)
				metricInfo["peak_utilization_pct"] = fmt.Sprintf("%.2f", peakPct)
				metricInfo["capacity_status"] = determineCapacityStatus(currentPct, 70.0, 85.0)
			}

			ifaceInfo[legend] = metricInfo
		}

		interfaceAnalysis[ifaceName] = ifaceInfo
	}

	return interfaceAnalysis, nil
}

func analyzeDiskCapacity(ctx context.Context, client *truenas.Client, timeRange string) (map[string]interface{}, error) {
	// Get available disk graphs
	graphsResult, err := client.CallContext(ctx, "reporting.graphs")
	if err != nil {
		return nil, fmt.Errorf("failed to query reporting graphs: %w", err)
	}

	var graphs []map[string]interface{}
	if err := json.Unmarshal(graphsResult, &graphs); err != nil {
		return nil, fmt.Errorf("failed to parse reporting graphs: %w", err)
	}

	// Find disk identifiers
	var diskIdentifiers []string
	for _, graph := range graphs {
		if graphName, ok := graph["name"].(string); ok && graphName == "disk" {
			if identifiersRaw, ok := graph["identifiers"]; ok && identifiersRaw != nil {
				if identifiersArray, ok := identifiersRaw.([]interface{}); ok {
					for _, idRaw := range identifiersArray {
						if idStr, ok := idRaw.(string); ok {
							diskIdentifiers = append(diskIdentifiers, idStr)
						}
					}
				}
			}
			break
		}
	}

	if len(diskIdentifiers) == 0 {
		return nil, fmt.Errorf("no disk identifiers found")
	}

	diskAnalysis := make(map[string]interface{})

	for _, identifier := range diskIdentifiers {
		diskName := identifier
		if idx := strings.Index(identifier, " |"); idx != -1 {
			diskName = identifier[:idx]
		}

		result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
			map[string]interface{}{
				"name":       "disk",
				"identifier": identifier,
			},
		}, map[string]interface{}{"unit": timeRange})

		if err != nil {
			diskAnalysis[diskName] = map[string]string{"error": err.Error()}
			continue
		}

		var metricsData []map[string]interface{}
		if err := json.Unmarshal(result, &metricsData); err != nil {
			diskAnalysis[diskName] = map[string]string{"error": fmt.Sprintf("parse error: %v", err)}
			continue
		}

		if len(metricsData) == 0 {
			continue
		}

		// Analyze I/O metrics (read/write operations and throughput)
		diskInfo := make(map[string]interface{})
		for _, metric := range metricsData {
			legend, _ := metric["legend"].(string)
			dataPoints, err := extractDataPoints(metric)
			if err != nil {
				continue
			}

			current := calculateRecentAverage(dataPoints, 5)
			average := calculateAverage(dataPoints)
			peak := calculateMax(dataPoints)
			trend := calculateTrendDirection(dataPoints)

			metricInfo := map[string]interface{}{
				"current": fmt.Sprintf("%.2f", current),
				"average": fmt.Sprintf("%.2f", average),
				"peak":    fmt.Sprintf("%.2f", peak),
				"trend":   trend,
			}

			diskInfo[legend] = metricInfo
		}

		diskAnalysis[diskName] = diskInfo
	}

	return diskAnalysis, nil
}

func handleGetPoolCapacityDetails(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	poolName, _ := args["pool_name"].(string)

	// Get pool information
	poolResult, err := client.CallContext(ctx, "pool.query")
	if err != nil {
		return "", err
	}

	var pools []map[string]interface{}
	if err := json.Unmarshal(poolResult, &pools); err != nil {
		return "", err
	}

	// Filter by pool name if specified
	var targetPools []map[string]interface{}
	for _, pool := range pools {
		if poolName == "" || pool["name"] == poolName {
			targetPools = append(targetPools, pool)
		}
	}

	analysis := make([]map[string]interface{}, 0, len(targetPools))

	for _, pool := range targetPools {
		poolAnalysis := make(map[string]interface{})

		poolAnalysis["name"] = pool["name"]
		poolAnalysis["status"] = pool["status"]
		poolAnalysis["healthy"] = pool["healthy"]

		// Get datasets for this pool
		var datasets []map[string]interface{}
		datasetResult, err := client.CallContext(ctx, "pool.dataset.query",
			[]interface{}{[]interface{}{"name", "^", pool["name"]}})
		if err == nil {
			if err := json.Unmarshal(datasetResult, &datasets); err == nil {
				poolAnalysis["datasets"] = analyzeDatasetCapacity(datasets)
			}
		}

		// Calculate capacity metrics from topology
		capacity := calculatePoolCapacity(pool)
		poolAnalysis["capacity"] = capacity

		// Determine warning level
		if utilPct, ok := capacity["utilization_pct"].(float64); ok {
			poolAnalysis["capacity_warning"] = determineCapacityStatus(utilPct, 70.0, 85.0)
		}

		analysis = append(analysis, poolAnalysis)
	}

	result := map[string]interface{}{
		"pools": analysis,
		"note":  "Historical capacity trends are not available from TrueNAS API. This shows current snapshot only. For growth trend analysis, query this tool periodically and track results externally.",
	}

	formatted, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// Helper functions for capacity analysis

func extractDataPoints(metric map[string]interface{}) ([]float64, error) {
	dataRaw, ok := metric["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("no data field in metric")
	}

	dataPoints := make([]float64, 0, len(dataRaw))
	for _, pointRaw := range dataRaw {
		if point, ok := pointRaw.([]interface{}); ok && len(point) >= 2 {
			if val, ok := point[1].(float64); ok {
				dataPoints = append(dataPoints, val)
			}
		}
	}

	if len(dataPoints) == 0 {
		return nil, fmt.Errorf("no valid data points")
	}

	return dataPoints, nil
}

func calculateAverage(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func calculateRecentAverage(values []float64, count int) float64 {
	if len(values) == 0 {
		return 0
	}

	start := len(values) - count
	if start < 0 {
		start = 0
	}

	return calculateAverage(values[start:])
}

func calculateMax(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	max := values[0]
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	return max
}

func calculateTrendDirection(values []float64) string {
	if len(values) < 2 {
		return "stable"
	}

	// Simple linear regression to determine trend
	n := float64(len(values))
	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumX2 := 0.0

	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	// Calculate slope
	slope := (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)

	// Determine trend based on slope
	avgValue := sumY / n
	if avgValue == 0 {
		return "stable"
	}

	// Calculate relative slope (% change per time unit)
	relativeSlope := (slope / avgValue) * 100

	if relativeSlope > 1.0 {
		return "increasing"
	} else if relativeSlope < -1.0 {
		return "decreasing"
	}
	return "stable"
}

func determineCapacityStatus(current, warningThreshold, criticalThreshold float64) string {
	if current >= criticalThreshold {
		return "critical"
	} else if current >= warningThreshold {
		return "warning"
	}
	return "healthy"
}

func calculateProjections(values []float64, current, warningThreshold, criticalThreshold float64) []string {
	projections := make([]string, 0)

	if len(values) < 2 {
		return projections
	}

	// Calculate growth rate (% per time unit)
	n := float64(len(values))
	sumX := 0.0
	sumY := 0.0
	sumXY := 0.0
	sumX2 := 0.0

	for i, y := range values {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	slope := (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)

	if slope <= 0 {
		return projections
	}

	// Project when we'll hit thresholds
	if current < warningThreshold {
		timeToWarning := (warningThreshold - current) / slope
		if timeToWarning > 0 && timeToWarning < 1000 {
			projections = append(projections, fmt.Sprintf("Warning threshold (%.0f%%) projected in ~%.0f time units", warningThreshold, timeToWarning))
		}
	}

	if current < criticalThreshold {
		timeToCritical := (criticalThreshold - current) / slope
		if timeToCritical > 0 && timeToCritical < 1000 {
			projections = append(projections, fmt.Sprintf("Critical threshold (%.0f%%) projected in ~%.0f time units", criticalThreshold, timeToCritical))
		}
	}

	return projections
}

func generateCapacityRecommendations(analysis map[string]interface{}) map[string]interface{} {
	recommendations := make([]string, 0)
	overallStatuses := make([]string, 0)

	// Check CPU
	if cpuAnalysis, ok := analysis["cpu"].(map[string]interface{}); ok {
		if status, ok := cpuAnalysis["capacity_status"].(string); ok {
			overallStatuses = append(overallStatuses, status)
			if status == "warning" {
				recommendations = append(recommendations,
					"CPU utilization is elevated (>70%). Consider reviewing workloads or planning CPU upgrade.")
			} else if status == "critical" {
				recommendations = append(recommendations,
					"CPU utilization is critical (>85%). Immediate action recommended: optimize workloads or upgrade hardware.")
			}
		}
	}

	// Check memory
	if memAnalysis, ok := analysis["memory"].(map[string]interface{}); ok {
		if status, ok := memAnalysis["capacity_status"].(string); ok {
			overallStatuses = append(overallStatuses, status)
			if status == "warning" {
				recommendations = append(recommendations,
					"Memory utilization is elevated (>70%). Consider adding more RAM or optimizing memory usage.")
			} else if status == "critical" {
				recommendations = append(recommendations,
					"Memory utilization is critical (>85%). Immediate action recommended: add more RAM or reduce workload.")
			}
		}
	}

	// Check network interfaces
	if netAnalysis, ok := analysis["network"].(map[string]interface{}); ok {
		for ifaceName, ifaceData := range netAnalysis {
			if ifaceName == "error" {
				continue
			}
			if ifaceInfo, ok := ifaceData.(map[string]interface{}); ok {
				for metric, metricData := range ifaceInfo {
					if metric == "link_speed_mbps" {
						continue
					}
					if metricInfo, ok := metricData.(map[string]interface{}); ok {
						if status, ok := metricInfo["capacity_status"].(string); ok {
							overallStatuses = append(overallStatuses, status)
							if status == "warning" || status == "critical" {
								recommendations = append(recommendations,
									fmt.Sprintf("Network interface %s (%s) is nearing capacity. Consider upgrading link speed or load balancing.", ifaceName, metric))
							}
						}
					}
				}
			}
		}
	}

	// Determine overall status
	overallStatus := "healthy"
	for _, status := range overallStatuses {
		if status == "critical" {
			overallStatus = "critical"
			break
		} else if status == "warning" {
			overallStatus = "warning"
		}
	}

	if len(recommendations) == 0 {
		recommendations = append(recommendations, "All monitored capacity metrics are within healthy ranges.")
	}

	return map[string]interface{}{
		"recommendations": recommendations,
		"overall_status":  overallStatus,
	}
}

func calculatePoolCapacity(pool map[string]interface{}) map[string]interface{} {
	capacity := make(map[string]interface{})

	// Try to get capacity from topology
	if topology, ok := pool["topology"].(map[string]interface{}); ok {
		// Look for data vdevs
		if data, ok := topology["data"].([]interface{}); ok && len(data) > 0 {
			totalBytes := int64(0)
			for _, vdevRaw := range data {
				if vdev, ok := vdevRaw.(map[string]interface{}); ok {
					if stats, ok := vdev["stats"].(map[string]interface{}); ok {
						if size, ok := stats["size"].(float64); ok {
							totalBytes += int64(size)
						}
					}
				}
			}
			if totalBytes > 0 {
				capacity["total_bytes"] = totalBytes
			}
		}
	}

	// Get used/available from root dataset if available
	if name, ok := pool["name"].(string); ok {
		capacity["pool_name"] = name
	}

	// Try to get usage from pool-level stats
	if usedBytes, ok := pool["allocated"].(float64); ok {
		capacity["used_bytes"] = int64(usedBytes)
	}

	if freeBytes, ok := pool["free"].(float64); ok {
		capacity["available_bytes"] = int64(freeBytes)
	}

	// Calculate utilization percentage
	if used, ok := capacity["used_bytes"].(int64); ok {
		if available, ok := capacity["available_bytes"].(int64); ok {
			total := used + available
			if total > 0 {
				utilPct := (float64(used) / float64(total)) * 100
				capacity["utilization_pct"] = utilPct
				capacity["total_bytes"] = total
			}
		}
	}

	return capacity
}

func analyzeDatasetCapacity(datasets []map[string]interface{}) []map[string]interface{} {
	analysis := make([]map[string]interface{}, 0, len(datasets))

	for _, ds := range datasets {
		dsAnalysis := map[string]interface{}{
			"name": ds["name"],
			"type": ds["type"],
		}

		// Get properties
		if props, ok := ds["properties"].(map[string]interface{}); ok {
			// Extract used space
			if used, ok := props["used"].(map[string]interface{}); ok {
				if usedVal, ok := used["rawvalue"].(string); ok {
					dsAnalysis["used_bytes"] = usedVal
				}
				if usedParsed, ok := used["parsed"].(float64); ok {
					dsAnalysis["used_bytes_numeric"] = int64(usedParsed)
				}
			}

			// Extract available space
			if available, ok := props["available"].(map[string]interface{}); ok {
				if availVal, ok := available["rawvalue"].(string); ok {
					dsAnalysis["available_bytes"] = availVal
				}
				if availParsed, ok := available["parsed"].(float64); ok {
					dsAnalysis["available_bytes_numeric"] = int64(availParsed)
				}
			}

			// Extract referenced space
			if referenced, ok := props["referenced"].(map[string]interface{}); ok {
				if refVal, ok := referenced["rawvalue"].(string); ok {
					dsAnalysis["referenced_bytes"] = refVal
				}
			}

			// Calculate utilization if we have both used and available
			if usedNum, usedOk := dsAnalysis["used_bytes_numeric"].(int64); usedOk {
				if availNum, availOk := dsAnalysis["available_bytes_numeric"].(int64); availOk {
					total := usedNum + availNum
					if total > 0 {
						utilPct := (float64(usedNum) / float64(total)) * 100
						dsAnalysis["utilization_pct"] = fmt.Sprintf("%.2f", utilPct)
					}
				}
			}
		}

		analysis = append(analysis, dsAnalysis)
	}

	return analysis
}
