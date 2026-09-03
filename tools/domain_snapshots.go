package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Snapshot tools: snapshot lifecycle and periodic snapshot tasks.
//
// The server could already list snapshots but not create, delete or roll one
// back, which left the most common recovery workflow ("take a snapshot before
// I change this, roll back if it breaks") outside the tool surface.

func (r *Registry) registerSnapshotsTools() {
	r.tools["create_snapshot"] = Tool{
		Definition: mcp.Tool{
			Name: "create_snapshot",
			Description: "Create a ZFS snapshot of a dataset or zvol. Use this before making risky changes so the state can be rolled back. " +
				"Set 'recursive' to also snapshot child datasets. Returns the full snapshot name (dataset@name).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dataset": map[string]interface{}{
						"type":        "string",
						"description": "Dataset or zvol to snapshot, e.g. 'tank/data'",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Snapshot name (the part after '@'), e.g. 'before-upgrade'",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "Also snapshot all child datasets (default false)",
					},
				},
				"required": []string{"dataset", "name"},
			},
		},
		Handler: handleCreateSnapshot,
	}

	r.tools["delete_snapshot"] = Tool{
		Definition: mcp.Tool{
			Name: "delete_snapshot",
			Description: "Delete a ZFS snapshot. This frees the space the snapshot was holding and cannot be undone. " +
				"Pass the full name in 'dataset@snapshot' form.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"snapshot": map[string]interface{}{
						"type":        "string",
						"description": "Full snapshot name, e.g. 'tank/data@before-upgrade'",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "Also delete identically named snapshots of child datasets (default false)",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview what would be deleted without deleting it",
					},
				},
				"required": []string{"snapshot"},
			},
		},
		Handler: r.handleDeleteSnapshotWithDryRun,
	}

	r.tools["rollback_snapshot"] = Tool{
		Definition: mcp.Tool{
			Name: "rollback_snapshot",
			Description: "Roll a dataset back to a snapshot. DESTRUCTIVE: every change made after that snapshot is discarded, " +
				"and with 'force' any newer snapshots are destroyed too. Always confirm with the user first, and prefer dry_run to see what would be lost.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"snapshot": map[string]interface{}{
						"type":        "string",
						"description": "Full snapshot name to roll back to, e.g. 'tank/data@before-upgrade'",
					},
					"force": map[string]interface{}{
						"type":        "boolean",
						"description": "Destroy snapshots newer than the target so the rollback can proceed (default false)",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Report which snapshots would be discarded without rolling back",
					},
				},
				"required": []string{"snapshot"},
			},
		},
		Handler: r.handleRollbackSnapshotWithDryRun,
	}

	r.tools["query_snapshot_tasks"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_snapshot_tasks",
			Description: "List periodic snapshot tasks: which datasets are snapshotted automatically, on what schedule, and how long each snapshot is kept. Useful for answering 'is this dataset actually being backed up?'",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dataset": map[string]interface{}{
						"type":        "string",
						"description": "Only return tasks covering this dataset",
					},
					"enabled_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Only return enabled tasks",
					},
					"limit": limitSchema(50),
				},
			},
		},
		Handler: handleQuerySnapshotTasks,
	}
}

func handleCreateSnapshot(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	dataset := strings.TrimSpace(stringArg(args, "dataset"))
	name := strings.TrimSpace(stringArg(args, "name"))

	if strings.Contains(dataset, "@") {
		return "", fmt.Errorf("dataset %q must not contain '@'; pass the snapshot name in the 'name' argument", dataset)
	}
	if strings.ContainsAny(name, "@/") {
		return "", fmt.Errorf("snapshot name %q must not contain '@' or '/'", name)
	}

	payload := map[string]interface{}{
		"dataset":   dataset,
		"name":      name,
		"recursive": boolArg(args, "recursive"),
	}

	raw, err := client.CallContext(ctx, "pool.snapshot.create", payload)
	if err != nil {
		return "", err
	}

	var created map[string]interface{}
	if err := json.Unmarshal(raw, &created); err != nil {
		// The snapshot exists even if we cannot parse the description of it.
		return marshalJSON(map[string]interface{}{
			"status":   "created",
			"snapshot": dataset + "@" + name,
		})
	}

	return marshalJSON(map[string]interface{}{
		"status":    "created",
		"snapshot":  dataset + "@" + name,
		"id":        created["id"],
		"created":   created["properties"],
		"recursive": boolArg(args, "recursive"),
	})
}

func (r *Registry) handleDeleteSnapshotWithDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return ExecuteWithDryRun(ctx, client, args, &deleteSnapshotDryRun{}, handleDeleteSnapshot)
}

func handleDeleteSnapshot(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	snapshot, err := requireSnapshotName(args)
	if err != nil {
		return "", err
	}

	options := map[string]interface{}{}
	if boolArg(args, "recursive") {
		options["recursive"] = true
	}

	if _, err := client.CallContext(ctx, "pool.snapshot.delete", snapshot, options); err != nil {
		return "", err
	}

	return marshalJSON(map[string]interface{}{
		"status":    "deleted",
		"snapshot":  snapshot,
		"recursive": boolArg(args, "recursive"),
	})
}

type deleteSnapshotDryRun struct{}

func (d *deleteSnapshotDryRun) ExecuteDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (*DryRunResult, error) {
	snapshot, err := requireSnapshotName(args)
	if err != nil {
		return nil, err
	}

	current, err := lookupSnapshot(ctx, client, snapshot)
	if err != nil {
		return nil, err
	}

	return &DryRunResult{
		Tool:         "delete_snapshot",
		CurrentState: current,
		PlannedActions: []PlannedAction{{
			Step:        1,
			Description: fmt.Sprintf("Destroy snapshot %s", snapshot),
			Operation:   "delete",
			Target:      snapshot,
			Details:     map[string]interface{}{"recursive": boolArg(args, "recursive")},
		}},
		Warnings: []string{
			"Deleting a snapshot is permanent; the data it was pinning becomes unrecoverable.",
		},
	}, nil
}

func (r *Registry) handleRollbackSnapshotWithDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return ExecuteWithDryRun(ctx, client, args, &rollbackSnapshotDryRun{}, handleRollbackSnapshot)
}

func handleRollbackSnapshot(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	snapshot, err := requireSnapshotName(args)
	if err != nil {
		return "", err
	}

	options := map[string]interface{}{}
	if boolArg(args, "force") {
		// "recursive" here means "destroy the newer snapshots that are in the
		// way", which is exactly what force implies for a rollback.
		options["recursive"] = true
		options["force"] = true
	}

	if _, err := client.CallContext(ctx, "pool.snapshot.rollback", snapshot, options); err != nil {
		return "", err
	}

	dataset, _, _ := strings.Cut(snapshot, "@")
	return marshalJSON(map[string]interface{}{
		"status":   "rolled_back",
		"dataset":  dataset,
		"snapshot": snapshot,
		"note":     "All changes made after this snapshot have been discarded.",
	})
}

type rollbackSnapshotDryRun struct{}

func (rb *rollbackSnapshotDryRun) ExecuteDryRun(ctx context.Context, client *truenas.Client, args map[string]interface{}) (*DryRunResult, error) {
	snapshot, err := requireSnapshotName(args)
	if err != nil {
		return nil, err
	}
	dataset, _, _ := strings.Cut(snapshot, "@")

	target, err := lookupSnapshot(ctx, client, snapshot)
	if err != nil {
		return nil, err
	}

	newer, err := snapshotsNewerThan(ctx, client, dataset, snapshot)
	if err != nil {
		return nil, err
	}

	warnings := []string{
		fmt.Sprintf("Every change written to %s after this snapshot will be discarded.", dataset),
	}
	if len(newer) > 0 && !boolArg(args, "force") {
		warnings = append(warnings, fmt.Sprintf(
			"%d newer snapshot(s) exist; the rollback will fail unless 'force' is set, which destroys them.", len(newer)))
	}
	if len(newer) > 0 && boolArg(args, "force") {
		warnings = append(warnings, fmt.Sprintf(
			"'force' is set: %d newer snapshot(s) will be destroyed: %s", len(newer), strings.Join(newer, ", ")))
	}

	return &DryRunResult{
		Tool:         "rollback_snapshot",
		CurrentState: map[string]interface{}{"target_snapshot": target, "newer_snapshots": newer},
		PlannedActions: []PlannedAction{{
			Step:        1,
			Description: fmt.Sprintf("Roll %s back to %s", dataset, snapshot),
			Operation:   "rollback",
			Target:      dataset,
			Details:     map[string]interface{}{"force": boolArg(args, "force")},
		}},
		Warnings: warnings,
	}, nil
}

func handleQuerySnapshotTasks(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return runQuery(ctx, client, args, queryOptions{
		Method:       "pool.snapshottask.query",
		Label:        "snapshot_tasks",
		DefaultLimit: 50,
		Fields: []string{
			"id", "dataset", "recursive", "lifetime_value", "lifetime_unit",
			"naming_schema", "schedule", "enabled", "state", "allow_empty", "exclude",
		},
		Filters: func(args map[string]interface{}) []interface{} {
			var filters []interface{}
			if ds := stringArg(args, "dataset"); ds != "" {
				filters = append(filters, []interface{}{"dataset", "=", ds})
			}
			if boolArg(args, "enabled_only") {
				filters = append(filters, []interface{}{"enabled", "=", true})
			}
			return filters
		},
	})
}

// requireSnapshotName validates the dataset@snapshot form up front. Passing a
// bare dataset name to pool.snapshot.delete would otherwise produce a
// middleware traceback rather than an actionable message.
func requireSnapshotName(args map[string]interface{}) (string, error) {
	snapshot := strings.TrimSpace(stringArg(args, "snapshot"))
	dataset, name, ok := strings.Cut(snapshot, "@")
	if !ok || dataset == "" || name == "" {
		return "", fmt.Errorf("snapshot %q must be in 'dataset@snapshot' form, e.g. 'tank/data@daily-2025-01-01'", snapshot)
	}
	return snapshot, nil
}

func lookupSnapshot(ctx context.Context, client *truenas.Client, snapshot string) (map[string]interface{}, error) {
	raw, err := client.CallContext(ctx, "pool.snapshot.query",
		[]interface{}{[]interface{}{"id", "=", snapshot}},
		map[string]interface{}{},
	)
	if err != nil {
		return nil, err
	}
	var found []map[string]interface{}
	if err := json.Unmarshal(raw, &found); err != nil {
		return nil, fmt.Errorf("failed to parse snapshot query response: %w", err)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("snapshot %q not found", snapshot)
	}
	return simplifySnapshot(found[0]), nil
}

// snapshotsNewerThan lists snapshots of dataset created after target, which is
// what a rollback would have to destroy.
func snapshotsNewerThan(ctx context.Context, client *truenas.Client, dataset, target string) ([]string, error) {
	raw, err := client.CallContext(ctx, "pool.snapshot.query",
		[]interface{}{[]interface{}{"dataset", "=", dataset}},
		map[string]interface{}{},
	)
	if err != nil {
		return nil, err
	}
	var all []map[string]interface{}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("failed to parse snapshot query response: %w", err)
	}

	targetCreation, ok := creationTime(all, target)
	if !ok {
		return nil, fmt.Errorf("snapshot %q not found on dataset %s", target, dataset)
	}

	var newer []string
	for _, snap := range all {
		id, _ := snap["id"].(string)
		if id == target {
			continue
		}
		if t, ok := snapshotCreation(snap); ok && t > targetCreation {
			newer = append(newer, id)
		}
	}
	return newer, nil
}

func creationTime(snapshots []map[string]interface{}, id string) (float64, bool) {
	for _, snap := range snapshots {
		if got, _ := snap["id"].(string); got == id {
			return snapshotCreation(snap)
		}
	}
	return 0, false
}

// snapshotCreation reads the ZFS "creation" property, which the middleware
// reports as a nested object with a parsed unix timestamp.
func snapshotCreation(snap map[string]interface{}) (float64, bool) {
	props, ok := snap["properties"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	creation, ok := props["creation"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	switch v := creation["parsed"].(type) {
	case float64:
		return v, true
	case map[string]interface{}:
		if ts, ok := v["$date"].(float64); ok {
			return ts, true
		}
	case string:
		return 0, false
	}
	if raw, ok := creation["rawvalue"].(string); ok {
		var ts float64
		if _, err := fmt.Sscanf(raw, "%f", &ts); err == nil {
			return ts, true
		}
	}
	return 0, false
}
