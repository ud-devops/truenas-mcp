package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Metrics tools: system, network, disk, ARC and UPS metrics.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerMetricsTools() {
	// System reporting metrics
	r.tools["get_system_metrics"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_system_metrics",
			Description: "Get system performance metrics (CPU, memory, load average, CPU temperature, uptime)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"graphs": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
							"enum": []string{"cpu", "cputemp", "memory", "load", "uptime"},
						},
						"description": "Metrics to retrieve (default: cpu, memory, load)",
					},
					"unit": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"HOUR", "DAY", "WEEK", "MONTH", "YEAR"},
						"description": "Time range for metrics (default: HOUR)",
						"default":     "HOUR",
					},
				},
			},
		},
		Handler: handleGetSystemMetrics,
	}

	// Network reporting metrics
	r.tools["get_network_metrics"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_network_metrics",
			Description: "Get network interface traffic metrics",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"interface": map[string]interface{}{
						"type":        "string",
						"description": "Network interface name (e.g., 'eth0'). If omitted, returns all interfaces.",
					},
					"unit": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"HOUR", "DAY", "WEEK", "MONTH", "YEAR"},
						"description": "Time range for metrics (default: HOUR)",
						"default":     "HOUR",
					},
				},
			},
		},
		Handler: handleGetNetworkMetrics,
	}

	// Disk I/O reporting metrics
	r.tools["get_disk_metrics"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_disk_metrics",
			Description: "Get disk performance metrics (I/O or temperature)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"disk": map[string]interface{}{
						"type":        "string",
						"description": "Disk name (e.g., 'sda'). If omitted, returns all disks.",
					},
					"type": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"disk", "disktemp"},
						"description": "Metric type: disk I/O or disk temperature (default: disk)",
						"default":     "disk",
					},
					"unit": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"HOUR", "DAY", "WEEK", "MONTH", "YEAR"},
						"description": "Time range for metrics (default: HOUR)",
						"default":     "HOUR",
					},
				},
			},
		},
		Handler: handleGetDiskMetrics,
	}

	// ZFS ARC reporting metrics
	r.tools["get_arc_metrics"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_arc_metrics",
			Description: "Get ZFS ARC (Adaptive Replacement Cache) performance metrics including cache size, demand hit/miss rates, and L2ARC statistics.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"graphs": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
							"enum": []string{
								"arcfreememory", "arcavailablememory", "arcsize",
								"demandaccessespersecond", "demanddataaccessespersecond", "demandmetadataaccessespersecond",
								"demanddatahitspersecond", "demanddataiohitspersecond", "demanddatamissespersecond",
								"demanddatahitpercentage", "demanddataiohitpercentage", "demanddatamisspercentage",
								"demandmetadatahitspersecond", "demandmetadataiohitspersecond", "demandmetadatamissespersecond",
								"demandmetadatahitpercentage", "demandmetadataiohitpercentage", "demandmetadatamisspercentage",
								"l2archhitspersecond", "l2arcmissespersecond", "totall2arcaccessespersecond",
								"l2architpercentage", "l2arcmisspercentage",
								"l2arcbytesreadpersecond", "l2arcbyteswrittenpersecond",
							},
						},
						"description": "ARC metrics to retrieve (default: arcfreememory, arcavailablememory, arcsize)",
					},
					"unit": map[string]interface{}{
						"type":    "string",
						"enum":    []string{"HOUR", "DAY", "WEEK", "MONTH", "YEAR"},
						"default": "HOUR",
					},
				},
			},
		},
		Handler: handleGetArcMetrics,
	}

	// UPS reporting metrics
	r.tools["get_ups_metrics"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_ups_metrics",
			Description: "Get UPS (Uninterruptible Power Supply) metrics. For upsvoltage, returns battery, input, and output voltage. Requires a UPS configured in TrueNAS.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"graphs": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
							"enum": []string{
								"upscharge", "upsruntime", "upsvoltage",
								"upscurrent", "upsfrequency", "upsload", "upstemperature",
							},
						},
						"description": "UPS metrics to retrieve (default: all)",
					},
					"unit": map[string]interface{}{
						"type":    "string",
						"enum":    []string{"HOUR", "DAY", "WEEK", "MONTH", "YEAR"},
						"default": "HOUR",
					},
				},
			},
		},
		Handler: handleGetUpsMetrics,
	}
}

// Reporting handlers

func handleGetSystemMetrics(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	unit := "HOUR"
	if u, ok := args["unit"].(string); ok && u != "" {
		unit = u
	}

	// Default graphs if not specified
	graphs := []string{"cpu", "memory", "load"}
	if g, ok := args["graphs"].([]interface{}); ok && len(g) > 0 {
		graphs = make([]string, len(g))
		for i, v := range g {
			if s, ok := v.(string); ok {
				graphs[i] = s
			}
		}
	}

	response := make(map[string]interface{})

	for _, graph := range graphs {
		var apiGraph string
		switch graph {
		case "cpu":
			apiGraph = "cpu"
		case "cputemp":
			apiGraph = "cputemp"
		case "memory":
			apiGraph = "memory"
		case "load":
			apiGraph = "load"
		case "uptime":
			apiGraph = "uptime"
		default:
			continue
		}

		result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
			map[string]interface{}{
				"name":       apiGraph,
				"identifier": nil,
			},
		}, map[string]interface{}{"unit": unit})
		if err != nil {
			response[graph] = map[string]string{"error": err.Error()}
			continue
		}

		var fullData []map[string]interface{}
		if err := json.Unmarshal(result, &fullData); err != nil {
			response[graph] = map[string]string{"error": fmt.Sprintf("parse error: %v", err)}
			continue
		}

		// Keep aggregations and metadata, but sample data points to reduce size
		summary := make(map[string]interface{})
		if len(fullData) > 0 {
			for key, value := range fullData[0] {
				if key == "data" {
					// Include sample of data points: first 10 and last 10
					if dataArray, ok := value.([]interface{}); ok {
						summary["data_points_total"] = len(dataArray)
						sample := make([]interface{}, 0)

						// First 10 points
						for i := 0; i < 10 && i < len(dataArray); i++ {
							sample = append(sample, dataArray[i])
						}

						// Last 10 points (if we have more than 20 total)
						if len(dataArray) > 20 {
							for i := len(dataArray) - 10; i < len(dataArray); i++ {
								sample = append(sample, dataArray[i])
							}
						}

						summary["data_sample"] = sample
					}
				} else {
					// Keep all other fields: aggregations, start, end, legend, name, identifier
					summary[key] = value
				}
			}
		}
		response[graph] = summary
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleGetNetworkMetrics(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	unit := "HOUR"
	if u, ok := args["unit"].(string); ok && u != "" {
		unit = u
	}

	iface, _ := args["interface"].(string)

	// If no interface specified, get all interfaces
	var interfaces []string
	if iface != "" {
		interfaces = []string{iface}
	} else {
		// Query for available network interfaces
		result, err := client.CallContext(ctx, "interface.query")
		if err != nil {
			return "", fmt.Errorf("failed to query interfaces: %w", err)
		}

		var ifaceList []map[string]interface{}
		if err := json.Unmarshal(result, &ifaceList); err != nil {
			return "", fmt.Errorf("failed to parse interface list: %w", err)
		}

		// Extract interface names
		for _, iface := range ifaceList {
			if name, ok := iface["name"].(string); ok && name != "" {
				interfaces = append(interfaces, name)
			}
		}

		if len(interfaces) == 0 {
			return `{"error": "no network interfaces found"}`, nil
		}
	}

	// Get metrics for each interface
	allMetrics := make(map[string]interface{})

	for _, ifaceName := range interfaces {
		result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
			map[string]interface{}{
				"name":       "interface",
				"identifier": ifaceName,
			},
		}, map[string]interface{}{"unit": unit})

		if err != nil {
			allMetrics[ifaceName] = map[string]string{"error": err.Error()}
			continue
		}

		var fullData []map[string]interface{}
		if err := json.Unmarshal(result, &fullData); err != nil {
			allMetrics[ifaceName] = map[string]string{"error": fmt.Sprintf("parse error: %v", err)}
			continue
		}

		// Keep aggregations and metadata, sample data points to reduce size
		summaries := make([]map[string]interface{}, 0, len(fullData))
		for _, item := range fullData {
			summary := make(map[string]interface{})
			for key, value := range item {
				if key == "data" {
					// Include sample: first 10 and last 10 data points
					if dataArray, ok := value.([]interface{}); ok {
						summary["data_points_total"] = len(dataArray)
						if len(dataArray) > 0 {
							sample := make([]interface{}, 0)

							for i := 0; i < 10 && i < len(dataArray); i++ {
								sample = append(sample, dataArray[i])
							}

							if len(dataArray) > 20 {
								for i := len(dataArray) - 10; i < len(dataArray); i++ {
									sample = append(sample, dataArray[i])
								}
							}

							summary["data_sample"] = sample
						}
					}
				} else {
					summary[key] = value
				}
			}
			summaries = append(summaries, summary)
		}

		if len(summaries) == 1 {
			allMetrics[ifaceName] = summaries[0]
		} else {
			allMetrics[ifaceName] = summaries
		}
	}

	formatted, err := json.MarshalIndent(allMetrics, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleGetDiskMetrics(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	unit := "HOUR"
	if u, ok := args["unit"].(string); ok && u != "" {
		unit = u
	}

	requestedDisk, _ := args["disk"].(string)

	graphType := "disk"
	if t, ok := args["type"].(string); ok && t != "" {
		graphType = t
	}

	// First, get available reporting graphs
	graphsResult, err := client.CallContext(ctx, "reporting.graphs")
	if err != nil {
		return "", fmt.Errorf("failed to query reporting graphs: %w", err)
	}

	var graphs []map[string]interface{}
	if err := json.Unmarshal(graphsResult, &graphs); err != nil {
		return "", fmt.Errorf("failed to parse reporting graphs: %w", err)
	}

	// Find the requested graph type and extract identifiers
	var diskIdentifiers []string
	for _, graph := range graphs {
		graphName, nameOk := graph["name"].(string)
		if nameOk && graphName == graphType {
			// Get the identifiers array
			if identifiersRaw, ok := graph["identifiers"]; ok && identifiersRaw != nil {
				if identifiersArray, ok := identifiersRaw.([]interface{}); ok {
					for _, idRaw := range identifiersArray {
						if idStr, ok := idRaw.(string); ok {
							// Extract disk name from identifier string (e.g., "sda | Type: SSD...")
							diskName := idStr
							if idx := strings.Index(idStr, " |"); idx != -1 {
								diskName = idStr[:idx]
							}

							// If specific disk requested, filter by name
							if requestedDisk == "" || diskName == requestedDisk {
								diskIdentifiers = append(diskIdentifiers, idStr)
							}
						}
					}
				}
			}
			break
		}
	}

	if len(diskIdentifiers) == 0 {
		return fmt.Sprintf(`{"error": "no disk identifiers found for graph type %q"}`, graphType), nil
	}

	// Get metrics for each disk identifier
	allMetrics := make(map[string]interface{})

	for _, identifier := range diskIdentifiers {
		// Extract disk name for the key (e.g., "sda" from "sda | Type: SSD...")
		diskName := identifier
		if idx := strings.Index(identifier, " |"); idx != -1 {
			diskName = identifier[:idx]
		}

		result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
			map[string]interface{}{
				"name":       graphType,
				"identifier": identifier,
			},
		}, map[string]interface{}{"unit": unit})

		if err != nil {
			allMetrics[diskName] = map[string]string{"error": err.Error()}
			continue
		}

		var fullData []map[string]interface{}
		if err := json.Unmarshal(result, &fullData); err != nil {
			allMetrics[diskName] = map[string]string{"error": fmt.Sprintf("parse error: %v", err)}
			continue
		}

		// Keep aggregations and metadata, sample data points to reduce size
		summaries := make([]map[string]interface{}, 0, len(fullData))
		for _, item := range fullData {
			summary := make(map[string]interface{})
			for key, value := range item {
				if key == "data" {
					// Include sample: first 10 and last 10 data points
					if dataArray, ok := value.([]interface{}); ok {
						summary["data_points_total"] = len(dataArray)
						if len(dataArray) > 0 {
							sample := make([]interface{}, 0)

							for i := 0; i < 10 && i < len(dataArray); i++ {
								sample = append(sample, dataArray[i])
							}

							if len(dataArray) > 20 {
								for i := len(dataArray) - 10; i < len(dataArray); i++ {
									sample = append(sample, dataArray[i])
								}
							}

							summary["data_sample"] = sample
						}
					}
				} else {
					summary[key] = value
				}
			}
			summaries = append(summaries, summary)
		}

		if len(summaries) == 1 {
			allMetrics[diskName] = summaries[0]
		} else {
			allMetrics[diskName] = summaries
		}
	}

	formatted, err := json.MarshalIndent(allMetrics, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleGetArcMetrics(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	unit := "HOUR"
	if u, ok := args["unit"].(string); ok && u != "" {
		unit = u
	}

	// Default graphs if not specified
	graphs := []string{"arcfreememory", "arcavailablememory", "arcsize"}
	if g, ok := args["graphs"].([]interface{}); ok && len(g) > 0 {
		graphs = make([]string, len(g))
		for i, v := range g {
			if s, ok := v.(string); ok {
				graphs[i] = s
			}
		}
	}

	response := make(map[string]interface{})

	for _, graph := range graphs {
		result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
			map[string]interface{}{
				"name":       graph,
				"identifier": nil,
			},
		}, map[string]interface{}{"unit": unit})
		if err != nil {
			response[graph] = map[string]string{"error": err.Error()}
			continue
		}

		var fullData []map[string]interface{}
		if err := json.Unmarshal(result, &fullData); err != nil {
			response[graph] = map[string]string{"error": fmt.Sprintf("parse error: %v", err)}
			continue
		}

		summary := make(map[string]interface{})
		if len(fullData) > 0 {
			for key, value := range fullData[0] {
				if key == "data" {
					if dataArray, ok := value.([]interface{}); ok {
						summary["data_points_total"] = len(dataArray)
						sample := make([]interface{}, 0)

						for i := 0; i < 10 && i < len(dataArray); i++ {
							sample = append(sample, dataArray[i])
						}

						if len(dataArray) > 20 {
							for i := len(dataArray) - 10; i < len(dataArray); i++ {
								sample = append(sample, dataArray[i])
							}
						}

						summary["data_sample"] = sample
					}
				} else {
					summary[key] = value
				}
			}
		}
		response[graph] = summary
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleGetUpsMetrics(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	unit := "HOUR"
	if u, ok := args["unit"].(string); ok && u != "" {
		unit = u
	}

	// Default to all UPS graphs
	graphs := []string{"upscharge", "upsruntime", "upsvoltage", "upscurrent", "upsfrequency", "upsload", "upstemperature"}
	if g, ok := args["graphs"].([]interface{}); ok && len(g) > 0 {
		graphs = make([]string, len(g))
		for i, v := range g {
			if s, ok := v.(string); ok {
				graphs[i] = s
			}
		}
	}

	response := make(map[string]interface{})

	for _, graph := range graphs {
		if graph == "upsvoltage" {
			// upsvoltage has per-identifier data (battery, input, output)
			// Discover identifiers from reporting.graphs
			graphsResult, err := client.CallContext(ctx, "reporting.graphs")
			if err != nil {
				response[graph] = map[string]string{"error": fmt.Sprintf("failed to query reporting graphs: %v", err)}
				continue
			}

			var reportingGraphs []map[string]interface{}
			if err := json.Unmarshal(graphsResult, &reportingGraphs); err != nil {
				response[graph] = map[string]string{"error": fmt.Sprintf("failed to parse reporting graphs: %v", err)}
				continue
			}

			var voltageIdentifiers []string
			for _, rg := range reportingGraphs {
				if name, ok := rg["name"].(string); ok && name == "upsvoltage" {
					if identifiersRaw, ok := rg["identifiers"]; ok && identifiersRaw != nil {
						if identifiersArray, ok := identifiersRaw.([]interface{}); ok {
							for _, idRaw := range identifiersArray {
								if idStr, ok := idRaw.(string); ok {
									voltageIdentifiers = append(voltageIdentifiers, idStr)
								}
							}
						}
					}
					break
				}
			}

			if len(voltageIdentifiers) == 0 {
				// No identifiers found — try nil identifier
				voltageIdentifiers = []string{""}
			}

			voltageData := make(map[string]interface{})
			for _, identifier := range voltageIdentifiers {
				var callIdentifier interface{}
				if identifier != "" {
					callIdentifier = identifier
				}
				result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
					map[string]interface{}{
						"name":       "upsvoltage",
						"identifier": callIdentifier,
					},
				}, map[string]interface{}{"unit": unit})
				if err != nil {
					voltageData[identifier] = map[string]string{"error": err.Error()}
					continue
				}

				var fullData []map[string]interface{}
				if err := json.Unmarshal(result, &fullData); err != nil {
					voltageData[identifier] = map[string]string{"error": fmt.Sprintf("parse error: %v", err)}
					continue
				}

				summary := make(map[string]interface{})
				if len(fullData) > 0 {
					for key, value := range fullData[0] {
						if key == "data" {
							if dataArray, ok := value.([]interface{}); ok {
								summary["data_points_total"] = len(dataArray)
								sample := make([]interface{}, 0)

								for i := 0; i < 10 && i < len(dataArray); i++ {
									sample = append(sample, dataArray[i])
								}

								if len(dataArray) > 20 {
									for i := len(dataArray) - 10; i < len(dataArray); i++ {
										sample = append(sample, dataArray[i])
									}
								}

								summary["data_sample"] = sample
							}
						} else {
							summary[key] = value
						}
					}
				}
				key := identifier
				if key == "" {
					key = "default"
				}
				voltageData[key] = summary
			}
			response[graph] = voltageData
		} else {
			// All other UPS graphs use nil identifier
			result, err := client.CallContext(ctx, "reporting.get_data", []interface{}{
				map[string]interface{}{
					"name":       graph,
					"identifier": nil,
				},
			}, map[string]interface{}{"unit": unit})
			if err != nil {
				response[graph] = map[string]string{"error": err.Error()}
				continue
			}

			var fullData []map[string]interface{}
			if err := json.Unmarshal(result, &fullData); err != nil {
				response[graph] = map[string]string{"error": fmt.Sprintf("parse error: %v", err)}
				continue
			}

			summary := make(map[string]interface{})
			if len(fullData) > 0 {
				for key, value := range fullData[0] {
					if key == "data" {
						if dataArray, ok := value.([]interface{}); ok {
							summary["data_points_total"] = len(dataArray)
							sample := make([]interface{}, 0)

							for i := 0; i < 10 && i < len(dataArray); i++ {
								sample = append(sample, dataArray[i])
							}

							if len(dataArray) > 20 {
								for i := len(dataArray) - 10; i < len(dataArray); i++ {
									sample = append(sample, dataArray[i])
								}
							}

							summary["data_sample"] = sample
						}
					} else {
						summary[key] = value
					}
				}
			}
			response[graph] = summary
		}
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}
