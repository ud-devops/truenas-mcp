package tools

import "github.com/truenas/truenas-mcp/mcp"

// toolAnnotations classifies every tool by what it can do to the system.
//
// This table is the single source of truth for --read-only. Read it as a
// safety policy, not as documentation: a tool marked ReadOnlyHint:true is one
// this server will expose to a model that has been told it may not change
// anything, so the bar for that mark is "cannot alter state on the NAS at
// all", not "usually harmless".
//
// DestructiveHint separates changes that are additive or trivially reversible
// (create a dataset, dismiss an alert) from changes that can lose data or take
// the system offline (delete a boot environment, reboot, leave a domain).
//
// A tool missing from this table is treated as destructive and non-read-only.
var toolAnnotations = map[string]mcp.ToolAnnotations{
	// --- System -------------------------------------------------------
	"system_info":   ro("System information"),
	"system_health": ro("System health"),
	"system_reboot": destructive("Reboot system"),

	// --- Updates ------------------------------------------------------
	"check_updates":   roOpenWorld("Check for updates"),
	"update_status":   ro("Update status"),
	"download_update": writeOpenWorld("Download update"),
	"apply_update":    destructive("Apply update"),

	// --- Boot environments --------------------------------------------
	"query_boot_environments":      ro("Query boot environments"),
	"get_current_boot_environment": ro("Current boot environment"),
	"delete_boot_environment":      destructive("Delete boot environment"),

	// --- Scrub --------------------------------------------------------
	"query_scrub_schedules": ro("Query scrub schedules"),
	"get_scrub_status":      ro("Scrub status"),
	"create_scrub_schedule": write("Create scrub schedule"),
	"run_scrub":             write("Run scrub"),
	"delete_scrub_schedule": destructive("Delete scrub schedule"),

	// --- Directory services -------------------------------------------
	"get_directory_service_status": ro("Directory service status"),
	"query_directory_services":     ro("Query directory services"),
	"list_directory_certificates":  ro("List directory certificates"),
	"refresh_directory_cache":      idempotentWrite("Refresh directory cache"),
	"configure_directory_service":  destructive("Configure directory service"),
	"leave_directory_service":      destructive("Leave directory service"),

	// --- Storage ------------------------------------------------------
	"query_pools":      ro("Query pools"),
	"query_datasets":   ro("Query datasets"),
	"query_snapshots":  ro("Query snapshots"),
	"query_shares":     ro("Query shares"),
	"create_dataset":   write("Create dataset"),
	"create_smb_share": write("Create SMB share"),
	"create_nfs_share": write("Create NFS share"),

	// --- Virtual machines ---------------------------------------------
	"query_vms": ro("Query virtual machines"),

	// --- Alerts -------------------------------------------------------
	"list_alerts":   ro("List alerts"),
	"dismiss_alert": write("Dismiss alert"),
	"restore_alert": write("Restore alert"),

	// --- Metrics ------------------------------------------------------
	"get_system_metrics":  ro("System metrics"),
	"get_network_metrics": ro("Network metrics"),
	"get_disk_metrics":    ro("Disk metrics"),
	"get_arc_metrics":     ro("ARC metrics"),
	"get_ups_metrics":     ro("UPS metrics"),

	// --- Applications --------------------------------------------------
	"query_apps":              ro("Query apps"),
	"get_app_config":          ro("Get app configuration"),
	"search_app_catalog":      roOpenWorld("Search app catalog"),
	"get_app_catalog_details": roOpenWorld("App catalog details"),
	"install_app":             writeOpenWorld("Install app"),
	"start_app":               write("Start app"),
	"stop_app":                destructive("Stop app"),
	"upgrade_app":             destructive("Upgrade app"),
	"update_app":              destructive("Update app configuration"),
	"delete_app":              destructive("Delete app"),

	// --- Snapshots -------------------------------------------------------
	"query_snapshot_tasks": ro("Query snapshot tasks"),
	"create_snapshot":      write("Create snapshot"),
	"delete_snapshot":      destructive("Delete snapshot"),
	"rollback_snapshot":    destructive("Roll back to snapshot"),

	// --- Infrastructure (read-only) ---------------------------------------
	"query_services":          ro("Query services"),
	"query_disks":             ro("Query disks"),
	"query_users":             ro("Query users"),
	"query_groups":            ro("Query groups"),
	"query_replication_tasks": ro("Query replication tasks"),
	"query_cloudsync_tasks":   ro("Query cloud sync tasks"),
	"query_certificates":      ro("Query certificates"),
	"query_iscsi_targets":     ro("Query iSCSI targets"),

	// --- Jobs, capacity, tasks ------------------------------------------
	"query_jobs":                ro("Query jobs"),
	"analyze_capacity":          ro("Analyze capacity"),
	"get_pool_capacity_details": ro("Pool capacity details"),
	"tasks_list":                ro("List tasks"),
	"tasks_get":                 ro("Get task"),
}

func ro(title string) mcp.ToolAnnotations {
	return mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    mcp.Ptr(true),
		DestructiveHint: mcp.Ptr(false),
		IdempotentHint:  mcp.Ptr(true),
		OpenWorldHint:   mcp.Ptr(false),
	}
}

func roOpenWorld(title string) mcp.ToolAnnotations {
	a := ro(title)
	a.OpenWorldHint = mcp.Ptr(true)
	return a
}

func write(title string) mcp.ToolAnnotations {
	return mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    mcp.Ptr(false),
		DestructiveHint: mcp.Ptr(false),
		IdempotentHint:  mcp.Ptr(false),
		OpenWorldHint:   mcp.Ptr(false),
	}
}

func idempotentWrite(title string) mcp.ToolAnnotations {
	a := write(title)
	a.IdempotentHint = mcp.Ptr(true)
	return a
}

func writeOpenWorld(title string) mcp.ToolAnnotations {
	a := write(title)
	a.OpenWorldHint = mcp.Ptr(true)
	return a
}

func destructive(title string) mcp.ToolAnnotations {
	return mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    mcp.Ptr(false),
		DestructiveHint: mcp.Ptr(true),
		IdempotentHint:  mcp.Ptr(false),
		OpenWorldHint:   mcp.Ptr(false),
	}
}
