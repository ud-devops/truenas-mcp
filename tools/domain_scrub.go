package tools

import (
	"github.com/truenas/truenas-mcp/mcp"
)

// Scrub tools: pool scrub scheduling.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerScrubTools() {
	// Pool scrub management
	r.tools["query_scrub_schedules"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_scrub_schedules",
			Description: "Query configured scrub schedules for all pools. Scrubs verify data integrity by reading all blocks and checking checksums, detecting and repairing silent corruption. Use filtering to view specific pools or only enabled schedules.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pool": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Filter by pool name (exact match)",
					},
					"enabled_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Show only enabled schedules (default: false)",
					},
				},
			},
		},
		Handler: handleQueryScrubSchedules,
	}

	r.tools["get_scrub_status"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_scrub_status",
			Description: "Get comprehensive scrub status for all pools. Combines schedule information, current scrub progress, and last scrub results. Use this to answer questions like 'when was tank last scrubbed?' or 'is a scrub running?'",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pool": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Specific pool name (omit for all pools)",
					},
				},
			},
		},
		Handler: handleGetScrubStatus,
	}

	r.tools["create_scrub_schedule"] = Tool{
		Definition: mcp.Tool{
			Name:        "create_scrub_schedule",
			Description: "Create automated scrub schedule for a pool. **Best practices**: Schedule weekly for production, monthly for home use. Run during off-peak hours (2-4am typical). Scrubs can take hours/days on large pools but are essential for data integrity.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pool": map[string]interface{}{
						"type":        "string",
						"description": "Required: Pool name to schedule scrubs for",
					},
					"schedule": map[string]interface{}{
						"type":        "object",
						"description": "Required: Cron schedule (e.g., {minute: '0', hour: '2', dow: '0'} for Sunday 2am)",
						"properties": map[string]interface{}{
							"minute": map[string]interface{}{
								"type":    "string",
								"default": "0",
							},
							"hour": map[string]interface{}{
								"type":    "string",
								"default": "0",
							},
							"dom": map[string]interface{}{
								"type":    "string",
								"default": "*",
							},
							"month": map[string]interface{}{
								"type":    "string",
								"default": "*",
							},
							"dow": map[string]interface{}{
								"type":    "string",
								"default": "*",
							},
						},
					},
					"threshold": map[string]interface{}{
						"type":        "integer",
						"description": "Optional: Days between scrubs (default: 35)",
						"default":     35,
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Human-readable description",
					},
					"enabled": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Enable immediately (default: true)",
						"default":     true,
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Preview without creating (default: false)",
						"default":     false,
					},
				},
				"required": []string{"pool", "schedule"},
			},
		},
		Handler: r.handleCreateScrubScheduleWithDryRun,
	}

	r.tools["run_scrub"] = Tool{
		Definition: mcp.Tool{
			Name:        "run_scrub",
			Description: "Manually start an immediate scrub on a pool. Returns task ID for progress tracking. **When to use**: Before critical backups, after hardware changes, when scheduled scrub was missed. Safe to run anytime but adds I/O load. Can be safely interrupted and resumed.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pool": map[string]interface{}{
						"type":        "string",
						"description": "Required: Pool name to scrub",
					},
					"threshold": map[string]interface{}{
						"type":        "integer",
						"description": "Optional: Skip if scrubbed within N days (default: 7)",
						"default":     7,
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Preview without starting (default: false)",
						"default":     false,
					},
				},
				"required": []string{"pool"},
			},
		},
		Handler: r.handleRunScrubWithDryRun,
	}

	r.tools["delete_scrub_schedule"] = Tool{
		Definition: mcp.Tool{
			Name:        "delete_scrub_schedule",
			Description: "Remove a scrub schedule. **IMPORTANT**: Pool will no longer have automatic scrubbing. Recommend running manual scrubs monthly if schedule is deleted. Consider updating schedule instead of deleting.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "integer",
						"description": "Required: Schedule ID to delete (from query_scrub_schedules)",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Preview without deleting (default: false)",
						"default":     false,
					},
				},
				"required": []string{"id"},
			},
		},
		Handler: r.handleDeleteScrubScheduleWithDryRun,
	}
}
