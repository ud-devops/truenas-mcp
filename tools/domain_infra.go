package tools

import (
	"context"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Infrastructure tools: read-only visibility into the parts of TrueNAS the
// server previously could not see at all.
//
// Every tool here is a query. Answering "which disks are failing?", "who has
// an account?", "is replication actually running?" required leaving the
// assistant and opening the web UI, which defeats the point of the server.

func (r *Registry) registerInfraTools() {
	r.tools["query_services"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_services",
			Description: "List TrueNAS services (SMB, NFS, iSCSI, SSH, SMART, ...) with their running state and whether they start at boot. Use this to answer 'is SMB running?' or 'why can nobody reach the share?'",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"service": map[string]interface{}{
						"type":        "string",
						"description": "Only return this service, e.g. 'cifs', 'nfs', 'ssh'",
					},
					"running_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Only return services that are currently running",
					},
					"limit": limitSchema(50),
				},
			},
		},
		Handler: handleQueryServices,
	}

	r.tools["query_disks"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_disks",
			Description: "List physical disks with model, serial, size, transfer mode and pool membership. Use this for 'which disk is in bay 3?', 'what is the serial of the failing disk?' or to find disks not assigned to any pool.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Device name, e.g. 'sda' or 'nvme0n1'",
					},
					"unassigned_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Only return disks that are not part of a pool",
					},
					"limit": limitSchema(100),
				},
			},
		},
		Handler: handleQueryDisks,
	}

	r.tools["query_users"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_users",
			Description: "List user accounts with UID, group membership, home directory, shell and whether the account is locked or built-in. Passwords and password hashes are never returned.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{
						"type":        "string",
						"description": "Match usernames containing this text (case-insensitive)",
					},
					"local_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Exclude accounts provided by a directory service",
					},
					"limit": limitSchema(100),
				},
			},
		},
		Handler: handleQueryUsers,
	}

	r.tools["query_groups"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_groups",
			Description: "List groups with GID, members and whether the group is built-in. Useful when working out why a share's permissions behave the way they do.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Match group names containing this text (case-insensitive)",
					},
					"local_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Exclude groups provided by a directory service",
					},
					"limit": limitSchema(100),
				},
			},
		},
		Handler: handleQueryGroups,
	}

	r.tools["query_replication_tasks"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_replication_tasks",
			Description: "List ZFS replication tasks with source and target datasets, transport, schedule, enabled state and the outcome of the last run. Use this to check whether off-site copies are actually succeeding.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Match task names containing this text (case-insensitive)",
					},
					"enabled_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Only return enabled tasks",
					},
					"limit": limitSchema(50),
				},
			},
		},
		Handler: handleQueryReplicationTasks,
	}

	r.tools["query_cloudsync_tasks"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_cloudsync_tasks",
			Description: "List cloud sync tasks (S3, B2, Google Drive, ...) with direction, transfer mode, schedule and last run state. Credentials are not returned.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Match task descriptions containing this text (case-insensitive)",
					},
					"enabled_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Only return enabled tasks",
					},
					"limit": limitSchema(50),
				},
			},
		},
		Handler: handleQueryCloudSyncTasks,
	}

	r.tools["query_certificates"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_certificates",
			Description: "List certificates with common name, subject alternative names, issuer and expiry date. Use this to find certificates that are about to expire.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Match certificate names containing this text (case-insensitive)",
					},
					"limit": limitSchema(50),
				},
			},
		},
		Handler: handleQueryCertificates,
	}

	r.tools["query_iscsi_targets"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_iscsi_targets",
			Description: "List iSCSI targets with their groups and associated extents. Use this when investigating block storage exported to hypervisors.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Match target names containing this text (case-insensitive)",
					},
					"limit": limitSchema(50),
				},
			},
		},
		Handler: handleQueryIscsiTargets,
	}
}

func handleQueryServices(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return runQuery(ctx, client, args, queryOptions{
		Method:       "service.query",
		Label:        "services",
		DefaultLimit: 50,
		Fields:       []string{"id", "service", "enable", "state", "pids"},
		Filters: func(args map[string]interface{}) []interface{} {
			var filters []interface{}
			if s := stringArg(args, "service"); s != "" {
				filters = append(filters, []interface{}{"service", "=", s})
			}
			if boolArg(args, "running_only") {
				filters = append(filters, []interface{}{"state", "=", "RUNNING"})
			}
			return filters
		},
	})
}

func handleQueryDisks(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return runQuery(ctx, client, args, queryOptions{
		Method:       "disk.query",
		Label:        "disks",
		DefaultLimit: 100,
		Fields: []string{
			"identifier", "name", "serial", "model", "size", "type",
			"rotationrate", "description", "pool", "devname", "zfs_guid",
		},
		Filters: func(args map[string]interface{}) []interface{} {
			var filters []interface{}
			if n := stringArg(args, "name"); n != "" {
				filters = append(filters, []interface{}{"name", "=", n})
			}
			if boolArg(args, "unassigned_only") {
				filters = append(filters, []interface{}{"pool", "=", nil})
			}
			return filters
		},
	})
}

func handleQueryUsers(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return runQuery(ctx, client, args, queryOptions{
		Method:       "user.query",
		Label:        "users",
		DefaultLimit: 100,
		// Deliberately narrow: user.query can return password hashes and SSH
		// keys, and none of that belongs in a model's context window.
		Fields: []string{
			"id", "uid", "username", "full_name", "group", "groups", "home",
			"shell", "builtin", "locked", "sudo_commands", "ssh_password_enabled", "local",
		},
		Filters: func(args map[string]interface{}) []interface{} {
			var filters []interface{}
			if u := stringArg(args, "username"); u != "" {
				filters = append(filters, containsFilter("username", u))
			}
			if boolArg(args, "local_only") {
				filters = append(filters, []interface{}{"local", "=", true})
			}
			return filters
		},
	})
}

func handleQueryGroups(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return runQuery(ctx, client, args, queryOptions{
		Method:       "group.query",
		Label:        "groups",
		DefaultLimit: 100,
		Fields:       []string{"id", "gid", "name", "builtin", "sudo_commands", "users", "local"},
		Filters: func(args map[string]interface{}) []interface{} {
			var filters []interface{}
			if n := stringArg(args, "name"); n != "" {
				filters = append(filters, containsFilter("name", n))
			}
			if boolArg(args, "local_only") {
				filters = append(filters, []interface{}{"local", "=", true})
			}
			return filters
		},
	})
}

func handleQueryReplicationTasks(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return runQuery(ctx, client, args, queryOptions{
		Method:       "replication.query",
		Label:        "replication_tasks",
		DefaultLimit: 50,
		Fields: []string{
			"id", "name", "direction", "transport", "source_datasets",
			"target_dataset", "recursive", "auto", "enabled", "schedule",
			"retention_policy", "state", "job",
		},
		Filters: func(args map[string]interface{}) []interface{} {
			var filters []interface{}
			if n := stringArg(args, "name"); n != "" {
				filters = append(filters, containsFilter("name", n))
			}
			if boolArg(args, "enabled_only") {
				filters = append(filters, []interface{}{"enabled", "=", true})
			}
			return filters
		},
	})
}

func handleQueryCloudSyncTasks(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return runQuery(ctx, client, args, queryOptions{
		Method:       "cloudsync.query",
		Label:        "cloudsync_tasks",
		DefaultLimit: 50,
		// "credentials" is excluded on purpose: it carries provider secrets.
		Fields: []string{
			"id", "description", "direction", "transfer_mode", "path",
			"enabled", "schedule", "snapshot", "state", "job", "attributes",
		},
		Filters: func(args map[string]interface{}) []interface{} {
			var filters []interface{}
			if d := stringArg(args, "description"); d != "" {
				filters = append(filters, containsFilter("description", d))
			}
			if boolArg(args, "enabled_only") {
				filters = append(filters, []interface{}{"enabled", "=", true})
			}
			return filters
		},
	})
}

func handleQueryCertificates(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return runQuery(ctx, client, args, queryOptions{
		Method:       "certificate.query",
		Label:        "certificates",
		DefaultLimit: 50,
		// The private key and CSR are excluded: they are secrets.
		Fields: []string{
			"id", "name", "common", "san", "issuer", "from", "until",
			"expired", "lifetime", "digest_algorithm", "key_type", "key_length", "cert_type",
		},
		Filters: func(args map[string]interface{}) []interface{} {
			if n := stringArg(args, "name"); n != "" {
				return []interface{}{containsFilter("name", n)}
			}
			return nil
		},
	})
}

func handleQueryIscsiTargets(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	return runQuery(ctx, client, args, queryOptions{
		Method:       "iscsi.target.query",
		Label:        "iscsi_targets",
		DefaultLimit: 50,
		Fields:       []string{"id", "name", "alias", "mode", "groups", "auth_networks"},
		Filters: func(args map[string]interface{}) []interface{} {
			if n := stringArg(args, "name"); n != "" {
				return []interface{}{containsFilter("name", n)}
			}
			return nil
		},
	})
}
