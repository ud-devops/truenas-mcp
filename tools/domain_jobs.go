package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Jobs tools: middleware job inspection.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerJobsTools() {
	// Query jobs
	r.tools["query_jobs"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_jobs",
			Description: "Query system jobs (running, pending, or completed tasks like replication, snapshots, scrubs, etc.)",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"state": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"RUNNING", "WAITING", "SUCCESS", "FAILED", "ABORTED", "all"},
						"description": "Filter by job state (default: RUNNING)",
						"default":     "RUNNING",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of jobs to return (default: 50)",
						"default":     50,
					},
				},
			},
		},
		Handler: handleQueryJobs,
	}
}

func handleQueryJobs(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	state := "RUNNING"
	if s, ok := args["state"].(string); ok && s != "" {
		state = s
	}

	limit := 50
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Build query filters based on state
	var filters []interface{}
	if state != "all" {
		filters = []interface{}{
			[]interface{}{"state", "=", state},
		}
	} else {
		filters = []interface{}{}
	}

	// Build options
	options := map[string]interface{}{
		"limit":    limit,
		"order_by": []string{"-time_started"}, // Most recent first
	}

	result, err := client.CallContext(ctx, "core.get_jobs", filters, options)
	if err != nil {
		return "", fmt.Errorf("failed to query jobs: %w", err)
	}

	var jobs []map[string]interface{}
	if err := json.Unmarshal(result, &jobs); err != nil {
		return "", fmt.Errorf("failed to parse jobs: %w", err)
	}

	// Create simplified response with relevant fields
	simplified := make([]map[string]interface{}, 0, len(jobs))
	for _, job := range jobs {
		jobInfo := map[string]interface{}{
			"id":          job["id"],
			"method":      job["method"],
			"state":       job["state"],
			"description": job["description"],
		}

		// Add optional fields if present
		if progress, ok := job["progress"]; ok && progress != nil {
			jobInfo["progress"] = progress
		}
		if timeStarted, ok := job["time_started"]; ok && timeStarted != nil {
			jobInfo["time_started"] = timeStarted
		}
		if timeFinished, ok := job["time_finished"]; ok && timeFinished != nil {
			jobInfo["time_finished"] = timeFinished
		}
		if result, ok := job["result"]; ok && result != nil {
			jobInfo["result"] = result
		}
		if errorMsg, ok := job["error"]; ok && errorMsg != nil {
			jobInfo["error"] = errorMsg
		}
		if exception, ok := job["exception"]; ok && exception != nil {
			jobInfo["exception"] = exception
		}
		if abortable, ok := job["abortable"]; ok {
			jobInfo["abortable"] = abortable
		}

		simplified = append(simplified, jobInfo)
	}

	response := map[string]interface{}{
		"jobs":         simplified,
		"job_count":    len(simplified),
		"state_filter": state,
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}
