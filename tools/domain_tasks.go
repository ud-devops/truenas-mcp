package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Tasks tools: long-running task tracking.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerTasksTools() {
	// Task management tools
	r.tools["tasks_list"] = Tool{
		Definition: mcp.Tool{
			Name:        "tasks_list",
			Description: "List all active and recent tasks. Tasks represent long-running operations like app upgrades.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"cursor": map[string]interface{}{
						"type":        "string",
						"description": "Pagination cursor from previous response",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of tasks to return (default: 50)",
						"default":     50,
					},
				},
			},
		},
		Handler: r.handleTasksList,
	}

	r.tools["tasks_get"] = Tool{
		Definition: mcp.Tool{
			Name:        "tasks_get",
			Description: "Get detailed status of a specific task by ID. Use this to track progress of long-running operations.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{
						"type":        "string",
						"description": "Task ID to retrieve",
					},
				},
				"required": []string{"task_id"},
			},
		},
		Handler: r.handleTasksGet,
	}
}

// handleTasksList lists all active and recent tasks
func (r *Registry) handleTasksList(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	cursor := ""
	if c, ok := args["cursor"].(string); ok {
		cursor = c
	}

	limit := 50
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	taskList, nextCursor, err := r.taskManager.List(cursor, limit)
	if err != nil {
		return "", fmt.Errorf("failed to list tasks: %w", err)
	}

	response := map[string]interface{}{
		"tasks": taskList,
	}
	if nextCursor != "" {
		response["next_cursor"] = nextCursor
	}

	formatted, _ := json.MarshalIndent(response, "", "  ")
	return string(formatted), nil
}

// handleTasksGet retrieves a specific task by ID
func (r *Registry) handleTasksGet(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	taskID, ok := args["task_id"].(string)
	if !ok || taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	task, err := r.taskManager.Get(taskID)
	if err != nil {
		return "", fmt.Errorf("failed to get task: %w", err)
	}

	formatted, _ := json.MarshalIndent(task, "", "  ")
	return string(formatted), nil
}
