package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/truenas/truenas-mcp/mcp"
	"github.com/truenas/truenas-mcp/truenas"
)

// Storage tools: pools, datasets, snapshots and shares.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerStorageTools() {
	// Storage pools query
	r.tools["query_pools"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_pools",
			Description: "Query storage pools with their status, capacity, and health information",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleQueryPools,
	}

	// Dataset query
	r.tools["query_datasets"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_datasets",
			Description: "Query datasets with optional filtering and sorting. Returns simplified dataset information with capacity, encryption status, and usage details. Use 'limit' to control result size, 'order_by' to sort by size, and 'encrypted_only' to filter.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pool": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Filter datasets by pool name",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Optional: Maximum number of datasets to return (default: 50 for manageable response size)",
					},
					"order_by": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Sort by 'used' (space usage), 'available', or 'name' (default: used descending)",
						"enum":        []string{"used", "available", "name"},
					},
					"encrypted_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Return only encrypted datasets (default: false)",
					},
				},
			},
		},
		Handler: handleQueryDatasets,
	}

	// Snapshots query
	r.tools["query_snapshots"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_snapshots",
			Description: "Query ZFS snapshots with optional filtering and sorting. Returns simplified snapshot information with creation info, dataset, and holds status. Use 'limit' to control result size, 'order_by' to sort.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dataset": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Filter snapshots by parent dataset name",
					},
					"pool": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Filter snapshots by pool name",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Optional: Maximum number of snapshots to return (default: 50 for manageable response size)",
					},
					"order_by": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Sort by 'name' (snapshot name, default descending), 'dataset' (parent dataset), or 'created' (parsed from name if available)",
						"enum":        []string{"name", "dataset", "created"},
					},
					"holds_only": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Return only snapshots with holds that prevent deletion (default: false)",
					},
				},
			},
		},
		Handler: handleQuerySnapshots,
	}

	// Shares query
	r.tools["query_shares"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_shares",
			Description: "Query SMB and NFS shares configuration",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"share_type": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"smb", "nfs", "all"},
						"description": "Type of shares to query (default: all)",
						"default":     "all",
					},
				},
			},
		},
		Handler: handleQueryShares,
	}

	// Dataset creation (write operation)
	r.tools["create_dataset"] = Tool{
		Definition: mcp.Tool{
			Name:        "create_dataset",
			Description: "Create a ZFS dataset (filesystem or volume) for storage. This tool is reusable for SMB shares, NFS exports, iSCSI LUNs, and application storage. Supports encryption, compression, quotas, and advanced ZFS features.\n\n**WIZARD GUIDANCE FOR LLM:**\nWhen helping users create datasets, ask these questions in order:\n\n1. **Pool Selection**: Query available pools first, ask which pool to use\n2. **Dataset Name**: Suggest format 'pool/shares/name' or 'pool/apps/name'\n3. **Dataset Type**: FILESYSTEM (default, for files) or VOLUME (for block storage/VMs)\n4. **Share Type Optimization** (if for sharing):\n   - SMB: Windows/Mac file shares (recommend for SMB shares)\n   - NFS: Unix/Linux file shares\n   - MULTIPROTOCOL: Both SMB and NFS access\n   - APPS: Application storage\n   - GENERIC: General purpose (default)\n5. **Encryption** (recommend for sensitive data):\n   - Ask: \"Is this for sensitive data?\"\n   - If yes: Recommend generate_key=true for simplicity\n   - If user wants passphrase: min 8 characters\n   - Algorithm: AES-256-GCM recommended\n6. **Compression**: LZ4 (recommended, balanced), ZSTD (modern), GZIP (higher compression), OFF\n7. **Space Quota** (optional): Ask if they want to limit size\n8. **ACL Type** (for SMB): NFSV4 (recommended for SMB/Windows), POSIX (Unix)\n9. **Advanced** (usually skip unless user asks):\n   - Deduplication: Warn about RAM overhead, recommend OFF\n   - Checksum, snapdir, atime, readonly\n\n**IMPORTANT RECOMMENDATIONS:**\n- For SMB shares: share_type=SMB, acltype=NFSV4, compression=LZ4\n- For NFS exports: share_type=NFS, acltype=POSIX, compression=LZ4\n- For multi-protocol: share_type=MULTIPROTOCOL, acltype=NFSV4\n- For apps: share_type=APPS, compression=LZ4 or ZSTD\n- Always recommend compression=LZ4 unless user has specific needs\n- Warn: Deduplication uses ~5GB RAM per TB, not recommended for most users\n- Warn: Encryption cannot be removed later, only option is to copy data elsewhere\n\n**BEFORE EXECUTING:**\n1. Use dry_run=true to preview the configuration\n2. Display summary showing: name, type, optimization, compression, encryption, quota, mountpoint\n3. Get explicit user confirmation with \"Shall I proceed?\"\n4. Warn: This is a WRITE operation creating permanent storage\n5. If encryption enabled, remind user to back up the key after creation\n\n**DRY RUN:**\nSet dry_run=true to preview what will be created without executing. Show user the preview, then ask for confirmation to proceed.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Dataset path including pool (e.g., 'tank/shares/documents' or 'pool/apps/immich')",
					},
					"type": map[string]interface{}{
						"type":        "string",
						"description": "FILESYSTEM (default, for files/directories) or VOLUME (for block storage/iSCSI/VMs)",
						"enum":        []string{"FILESYSTEM", "VOLUME"},
						"default":     "FILESYSTEM",
					},
					"volsize": map[string]interface{}{
						"type":        "integer",
						"description": "Required for VOLUME type: size in bytes (e.g., 1099511627776 for 1TB)",
					},
					"share_type": map[string]interface{}{
						"type":        "string",
						"description": "Optimization hint: GENERIC (default), SMB, NFS, MULTIPROTOCOL, APPS",
						"enum":        []string{"GENERIC", "SMB", "NFS", "MULTIPROTOCOL", "APPS"},
					},
					"compression": map[string]interface{}{
						"type":        "string",
						"description": "LZ4 (recommended, balanced), ZSTD (modern), GZIP (higher compression), OFF, or INHERIT (default)",
						"enum":        []string{"LZ4", "ZSTD", "GZIP", "GZIP-1", "GZIP-9", "OFF", "INHERIT"},
					},
					"acltype": map[string]interface{}{
						"type":        "string",
						"description": "NFSV4 (recommended for SMB/Windows ACLs) or POSIX (Unix permissions)",
						"enum":        []string{"NFSV4", "POSIX", "INHERIT"},
					},
					"encryption_options": map[string]interface{}{
						"type":        "object",
						"description": "Encryption configuration (cannot be removed later)",
						"properties": map[string]interface{}{
							"generate_key": map[string]interface{}{
								"type":        "boolean",
								"description": "Auto-generate encryption key (recommended for simplicity)",
							},
							"passphrase": map[string]interface{}{
								"type":        "string",
								"description": "User passphrase (min 8 chars) - alternative to generate_key",
							},
							"algorithm": map[string]interface{}{
								"type":        "string",
								"description": "Encryption algorithm (default: AES-256-GCM recommended)",
								"enum":        []string{"AES-128-CCM", "AES-192-CCM", "AES-256-CCM", "AES-128-GCM", "AES-192-GCM", "AES-256-GCM"},
							},
						},
					},
					"quota": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum space for dataset + children in bytes (e.g., 1099511627776 for 1TB)",
					},
					"refquota": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum space for dataset only (excluding children) in bytes",
					},
					"create_ancestors": map[string]interface{}{
						"type":        "boolean",
						"description": "Auto-create missing parent datasets (default: true)",
						"default":     true,
					},
					"readonly": map[string]interface{}{
						"type":        "boolean",
						"description": "Make dataset read-only (default: false)",
						"default":     false,
					},
					"deduplication": map[string]interface{}{
						"type":        "string",
						"description": "OFF (recommended), ON, or VERIFY. Warning: Uses ~5GB RAM per TB of storage",
						"enum":        []string{"OFF", "ON", "VERIFY", "INHERIT"},
					},
					"checksum": map[string]interface{}{
						"type":        "string",
						"description": "Data integrity algorithm: SHA256 (default), BLAKE3, SHA512, etc.",
					},
					"snapdir": map[string]interface{}{
						"type":        "string",
						"description": "Snapshot directory visibility: VISIBLE or HIDDEN",
						"enum":        []string{"VISIBLE", "HIDDEN", "INHERIT"},
					},
					"atime": map[string]interface{}{
						"type":        "string",
						"description": "File access time tracking: ON or OFF (OFF improves performance)",
						"enum":        []string{"ON", "OFF", "INHERIT"},
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview what will be created without executing (default: false)",
						"default":     false,
					},
				},
				"required": []string{"name"},
			},
		},
		Handler: handleCreateDataset,
	}

	// SMB share creation (write operation)
	r.tools["create_smb_share"] = Tool{
		Definition: mcp.Tool{
			Name:        "create_smb_share",
			Description: "Create an SMB (Windows/macOS file sharing) share. This makes a ZFS dataset accessible over the network via the SMB/CIFS protocol.\n\n**WIZARD GUIDANCE FOR LLM:**\nWhen helping users create SMB shares, follow this conversation flow:\n\n**1. Dataset Selection:**\n- Ask: \"Do you want to create a new dataset or use an existing ZFS dataset?\"\n- If NEW: Use create_dataset tool first (with share_type=SMB, acltype=NFSV4)\n- If EXISTING: \n  * Query available datasets first with query_datasets\n  * Present options to user (NEVER suggest pool root like 'tank' or 'flash')\n  * Use the dataset's mountpoint as the path\n  * Warn: \"Never share a pool root - always use a child dataset\"\n- After dataset creation, use its mountpoint as the path\n\n**2. Share Name:**\n- Ask: \"What name should appear when browsing the network?\"\n- Rules: Max 80 chars, no \\ / [ ] : | < > + = ; , * ? \"\n- Cannot use: global, printers, homes\n- Suggest: Use a friendly, descriptive name like \"TeamDocs\" or \"PhotoArchive\"\n\n**3. Description:**\n- Ask: \"Add a description?\" (optional, shown when browsing shares)\n\n**4. Purpose Selection:**\n- Ask: \"What's this share for?\"\n- Options:\n  * DEFAULT_SHARE: Standard file sharing (most common)\n  * TIMEMACHINE_SHARE: macOS Time Machine backups\n  * MULTIPROTOCOL_SHARE: Both SMB and NFS access (complex permissions)\n  * PRIVATE_DATASETS_SHARE: User home directories\n  * VEEAM_REPOSITORY_SHARE: Veeam backup storage\n- Recommend DEFAULT_SHARE unless specific use case\n\n**5. Access Control:**\n- Ask: \"Read-only or read-write?\" (default: read-write)\n- Ask: \"Should it be visible when browsing?\" (default: yes)\n- Ask: \"Restrict to specific IP addresses?\" (optional, for hostsallow)\n- Ask: \"Hide from unauthorized users?\" (access_based_share_enumeration)\n\n**6. Purpose-Specific Questions:**\n\nFor TIMEMACHINE_SHARE:\n- Ask: \"What's the backup size limit?\" (recommend 2-3x Mac's disk size)\n- Set time_machine_quota in options\n\nFor MULTIPROTOCOL_SHARE:\n- Warn: \"Multi-protocol shares have complex permission interactions\"\n- Recommend: \"Use either SMB OR NFS, not both, unless you understand the implications\"\n\nFor PRIVATE_DATASETS_SHARE:\n- Suggest: \"Create separate datasets per user for isolation\"\n- Recommend: \"Use access_based_share_enumeration=true\"\n\n**7. Auditing (Optional):**\n- Ask: \"Enable access auditing?\" (tracks who accesses files)\n- If yes: Ask which groups to audit (empty = audit all)\n\n**IMPORTANT RECOMMENDATIONS:**\n- Default: enabled=true, browsable=true, readonly=false\n- For sensitive data: Set access_based_share_enumeration=true\n- For public shares: Use hostsdeny to block unwanted networks\n- For Time Machine: Set appropriate quota to prevent filling pool\n- For multi-protocol: Strongly recommend against unless necessary\n\n**SECURITY WARNINGS TO DISPLAY:**\n- If browsable=true + no hostsallow: \"Share visible and accessible from any network\"\n- If readonly=false: \"Users can modify, delete, and create files\"\n- If no access restrictions: \"Anyone on your network can access this share\"\n- Remind: \"Configure share permissions in TrueNAS UI after creation\"\n\n**BEFORE EXECUTING:**\n1. Use dry_run=true to preview the configuration\n2. Display complete summary including:\n   - Share name and network path (\\\\truenas\\sharename)\n   - Local path\n   - Purpose and access settings\n   - Security warnings if applicable\n3. Get explicit user confirmation: \"Shall I create this share?\"\n4. Warn: \"This is a WRITE operation that exposes data over your network\"\n5. After creation: Remind user to configure permissions via TrueNAS UI\n\n**DRY RUN:**\nSet dry_run=true to preview what will be created without executing. Show user the preview including security warnings, then ask for confirmation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Share name visible to clients (max 80 chars, case-insensitive, must be unique)",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "ZFS dataset mountpoint starting with /mnt/ (e.g., /mnt/tank/shares/docs, NOT /mnt/tank). Use 'EXTERNAL' only for DFS proxy shares.",
					},
					"purpose": map[string]interface{}{
						"type":        "string",
						"description": "Share purpose: DEFAULT_SHARE (standard), TIMEMACHINE_SHARE (macOS backups), MULTIPROTOCOL_SHARE (SMB+NFS), PRIVATE_DATASETS_SHARE (home dirs)",
						"enum":        []string{"DEFAULT_SHARE", "LEGACY_SHARE", "TIMEMACHINE_SHARE", "MULTIPROTOCOL_SHARE", "TIME_LOCKED_SHARE", "PRIVATE_DATASETS_SHARE", "EXTERNAL_SHARE", "VEEAM_REPOSITORY_SHARE", "FCP_SHARE"},
						"default":     "DEFAULT_SHARE",
					},
					"enabled": map[string]interface{}{
						"type":        "boolean",
						"description": "Enable share for network access (default: true)",
						"default":     true,
					},
					"comment": map[string]interface{}{
						"type":        "string",
						"description": "Description shown when clients list shares (optional)",
					},
					"readonly": map[string]interface{}{
						"type":        "boolean",
						"description": "Prevent clients from creating/modifying files (default: false)",
						"default":     false,
					},
					"browsable": map[string]interface{}{
						"type":        "boolean",
						"description": "Show share in network browse lists (default: true)",
						"default":     true,
					},
					"access_based_share_enumeration": map[string]interface{}{
						"type":        "boolean",
						"description": "Hide share from users without filesystem ACL access (default: false)",
						"default":     false,
					},
					"hostsallow": map[string]interface{}{
						"type":        "array",
						"description": "IP addresses/networks allowed to access (empty = allow all)",
						"items": map[string]interface{}{
							"type": "string",
						},
					},
					"hostsdeny": map[string]interface{}{
						"type":        "array",
						"description": "IP addresses/networks denied access (empty = deny none)",
						"items": map[string]interface{}{
							"type": "string",
						},
					},
					"audit": map[string]interface{}{
						"type":        "object",
						"description": "Audit configuration for tracking file access",
						"properties": map[string]interface{}{
							"enable": map[string]interface{}{
								"type":        "boolean",
								"description": "Enable audit logging",
							},
							"watch_list": map[string]interface{}{
								"type":        "array",
								"description": "Groups to audit (empty = audit all)",
								"items": map[string]interface{}{
									"type": "string",
								},
							},
							"ignore_list": map[string]interface{}{
								"type":        "array",
								"description": "Groups to exclude from auditing",
								"items": map[string]interface{}{
									"type": "string",
								},
							},
						},
					},
					"options": map[string]interface{}{
						"type":        "object",
						"description": "Purpose-specific options (varies by purpose)",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview what will be created without executing (default: false)",
						"default":     false,
					},
				},
				"required": []string{"name", "path"},
			},
		},
		Handler: handleCreateSMBShare,
	}

	// NFS share creation (write operation)
	r.tools["create_nfs_share"] = Tool{
		Definition: mcp.Tool{
			Name:        "create_nfs_share",
			Description: "Create an NFS (Network File System) share for Unix/Linux file sharing. This makes a ZFS dataset accessible over the network via the NFS protocol.\n\n**WIZARD GUIDANCE FOR LLM:**\nWhen helping users create NFS shares, follow this conversation flow:\n\n**1. Dataset Selection:**\n- Ask: \"Do you want to create a new dataset or use an existing ZFS dataset?\"\n- If NEW: Use create_dataset tool first (with share_type=NFS, acltype=POSIX)\n- If EXISTING: \n  * Query available datasets first with query_datasets\n  * Present options to user (NEVER suggest pool root like 'tank' or 'flash')\n  * Use the dataset's mountpoint as the path\n  * Warn: \"Never share a pool root - always use a child dataset\"\n- After dataset creation, use its mountpoint as the path\n\n**2. Access Control:**\n- Ask: \"Read-only or read-write?\" (default: read-write)\n- Ask: \"Restrict to specific networks?\" (CIDR notation: 192.168.1.0/24)\n- Ask: \"Restrict to specific hosts?\" (IP addresses or hostnames)\n- Recommend: At least one restriction (network or host) for security\n\n**3. User Mapping (Important for Security):**\n- Ask: \"How should root access be handled?\"\n  * **maproot_user**: Map root clients to specific user (recommended: 'nobody')\n  * **maproot_group**: Map root clients to specific group (recommended: 'nogroup')\n  * Warn if not set: \"Root clients will have full root access (security risk)\"\n- Ask: \"Map all users to a specific user?\" (optional, for anonymous access)\n  * **mapall_user**: Maps all clients to one user\n  * **mapall_group**: Maps all client groups to one group\n\n**4. Security Level (Optional):**\n- Default: SYS (system authentication)\n- Advanced: KRB5, KRB5I, KRB5P (Kerberos, requires setup)\n- Usually skip unless user specifically needs Kerberos\n\n**IMPORTANT RECOMMENDATIONS:**\n- For NFS shares: share_type=NFS, acltype=POSIX (in dataset creation)\n- Compression: LZ4 recommended for balanced performance\n- Always set maproot_user='nobody' to prevent root access\n- Use network/host restrictions to limit access\n- Read-only for shared data that shouldn't be modified\n\n**SECURITY WARNINGS TO DISPLAY:**\n- If no network/host restrictions: \"Share accessible from any host\"\n- If no maproot_user: \"Root clients will have full root access\"\n- If read-write + no restrictions: \"Any host can modify/delete files\"\n- Remind: \"Ensure NFS service is running and firewall allows NFS traffic (port 2049)\"\n\n**BEFORE EXECUTING:**\n1. Use dry_run=true to preview the configuration\n2. Display complete summary including:\n   - Local path\n   - Access type (read-only/read-write)\n   - Network/host restrictions\n   - User mapping settings\n   - Security warnings if applicable\n3. Get explicit user confirmation: \"Shall I create this NFS share?\"\n4. Warn: \"This is a WRITE operation that exposes data over your network\"\n5. After creation: Provide mount command example\n\n**DRY RUN:**\nSet dry_run=true to preview what will be created without executing. Show user the preview including security warnings, then ask for confirmation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "ZFS dataset mountpoint starting with /mnt/ (e.g., /mnt/tank/shares/data, NOT /mnt/tank)",
					},
					"enabled": map[string]interface{}{
						"type":        "boolean",
						"description": "Enable share for network access (default: true)",
						"default":     true,
					},
					"comment": map[string]interface{}{
						"type":        "string",
						"description": "Description for the share (optional)",
					},
					"ro": map[string]interface{}{
						"type":        "boolean",
						"description": "Read-only export (default: false for read-write)",
						"default":     false,
					},
					"networks": map[string]interface{}{
						"type":        "array",
						"description": "Authorized networks in CIDR notation (e.g., ['192.168.1.0/24']). Empty = allow all networks.",
						"items": map[string]interface{}{
							"type": "string",
						},
					},
					"hosts": map[string]interface{}{
						"type":        "array",
						"description": "Authorized IP addresses or hostnames (e.g., ['192.168.1.10', 'client.local']). No quotes or spaces. Empty = allow all hosts.",
						"items": map[string]interface{}{
							"type": "string",
						},
					},
					"maproot_user": map[string]interface{}{
						"type":        "string",
						"description": "Map root clients to this user (recommended: 'nobody' for security)",
					},
					"maproot_group": map[string]interface{}{
						"type":        "string",
						"description": "Map root clients to this group (recommended: 'nogroup' for security)",
					},
					"mapall_user": map[string]interface{}{
						"type":        "string",
						"description": "Map all clients to this user (optional, for anonymous access)",
					},
					"mapall_group": map[string]interface{}{
						"type":        "string",
						"description": "Map all client groups to this group (optional, for anonymous access)",
					},
					"security": map[string]interface{}{
						"type":        "array",
						"description": "Security mechanisms: ['SYS'] (default), ['KRB5'], ['KRB5I'], ['KRB5P']",
						"items": map[string]interface{}{
							"type": "string",
							"enum": []string{"SYS", "KRB5", "KRB5I", "KRB5P"},
						},
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview what will be created without executing (default: false)",
						"default":     false,
					},
				},
				"required": []string{"path"},
			},
		},
		Handler: handleCreateNFSShare,
	}
}

func handleQueryPools(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	result, err := client.CallContext(ctx, "pool.query")
	if err != nil {
		return "", err
	}

	var pools []map[string]interface{}
	if err := json.Unmarshal(result, &pools); err != nil {
		return "", fmt.Errorf("failed to parse pools (raw response: %s): %w", string(result), err)
	}

	formatted, err := json.MarshalIndent(pools, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleQueryDatasets(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	// Build query filters - initialize as empty array, not nil (API expects [] not null)
	filters := []interface{}{}
	if pool, ok := args["pool"].(string); ok && pool != "" {
		filters = []interface{}{
			[]interface{}{"name", "^", pool},
		}
	}

	// Options parameter (required by API even if empty)
	options := map[string]interface{}{}

	result, err := client.CallContext(ctx, "pool.dataset.query", filters, options)
	if err != nil {
		return "", err
	}

	var datasets []map[string]interface{}
	if err := json.Unmarshal(result, &datasets); err != nil {
		return "", fmt.Errorf("failed to parse datasets: %w", err)
	}

	// Simplify response
	simplified := make([]map[string]interface{}, 0, len(datasets))
	for _, ds := range datasets {
		summary := simplifyDataset(ds)
		simplified = append(simplified, summary)
	}

	// Filter by encryption status if requested
	if encryptedOnly, ok := args["encrypted_only"].(bool); ok && encryptedOnly {
		filtered := make([]map[string]interface{}, 0)
		for _, ds := range simplified {
			if encrypted, ok := ds["encrypted"].(bool); ok && encrypted {
				filtered = append(filtered, ds)
			}
		}
		simplified = filtered
	}

	// Sort datasets
	orderBy := "used" // default to sorting by space usage
	if order, ok := args["order_by"].(string); ok && order != "" {
		orderBy = order
	}
	sortDatasets(simplified, orderBy)

	// Apply limit (default to 50 for manageable response size)
	limit := 50
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	if len(simplified) > limit {
		simplified = simplified[:limit]
	}

	// Add metadata wrapper
	response := map[string]interface{}{
		"datasets":       simplified,
		"dataset_count":  len(simplified),
		"total_datasets": len(datasets),
	}
	if pool, ok := args["pool"].(string); ok && pool != "" {
		response["pool_filter"] = pool
	}
	if len(simplified) < len(datasets) {
		response["note"] = fmt.Sprintf("Showing %d of %d datasets (limited)", len(simplified), len(datasets))
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// simplifyDataset extracts the most relevant fields from a raw dataset object
func simplifyDataset(ds map[string]interface{}) map[string]interface{} {
	summary := map[string]interface{}{
		"name": ds["name"],
		"type": ds["type"],
		"pool": ds["pool"],
	}

	// Helper to extract parsed value from property object
	getParsed := func(prop interface{}) interface{} {
		if propMap, ok := prop.(map[string]interface{}); ok {
			return propMap["parsed"]
		}
		return nil
	}

	// Helper to extract human-readable value from property object
	getValue := func(prop interface{}) interface{} {
		if propMap, ok := prop.(map[string]interface{}); ok {
			if val := propMap["value"]; val != nil {
				return val
			}
			return propMap["parsed"]
		}
		return nil
	}

	// Mountpoint (direct field, not nested)
	if mp, ok := ds["mountpoint"].(string); ok && mp != "" {
		summary["mountpoint"] = mp
	}

	// Capacity fields (CRITICAL for user queries)
	if used := getParsed(ds["used"]); used != nil {
		summary["used_bytes"] = used
		summary["used"] = getValue(ds["used"]) // Human readable like "1008.3 GiB"
	}
	if avail := getParsed(ds["available"]); avail != nil {
		summary["available_bytes"] = avail
		summary["available"] = getValue(ds["available"]) // Human readable like "5.87 TiB"
	}

	// Usage breakdown (useful for understanding where space goes)
	if snapUsed := getParsed(ds["usedbysnapshots"]); snapUsed != nil {
		if bytes, ok := snapUsed.(float64); ok && bytes > 0 {
			summary["used_by_snapshots"] = getValue(ds["usedbysnapshots"])
		}
	}
	if dsUsed := getParsed(ds["usedbydataset"]); dsUsed != nil {
		summary["used_by_dataset"] = getValue(ds["usedbydataset"])
	}
	if childUsed := getParsed(ds["usedbychildren"]); childUsed != nil {
		if bytes, ok := childUsed.(float64); ok && bytes > 0 {
			summary["used_by_children"] = getValue(ds["usedbychildren"])
		}
	}

	// Compression
	if comp := getParsed(ds["compression"]); comp != nil {
		summary["compression"] = comp
		if ratio := getParsed(ds["compressratio"]); ratio != nil {
			summary["compression_ratio"] = ratio
		}
	}

	// Deduplication (only if enabled)
	if dedup := getParsed(ds["deduplication"]); dedup != nil {
		if dedupStr, ok := dedup.(string); ok && dedupStr != "off" {
			summary["deduplication"] = dedup
		}
	}

	// Quotas (only if set)
	if quota := getParsed(ds["quota"]); quota != nil {
		summary["quota"] = getValue(ds["quota"])
	}
	if refquota := getParsed(ds["refquota"]); refquota != nil {
		summary["refquota"] = getValue(ds["refquota"])
	}

	// Encryption
	if encrypted, ok := ds["encrypted"].(bool); ok {
		summary["encrypted"] = encrypted
		if encrypted {
			if locked, ok := ds["locked"].(bool); ok {
				summary["locked"] = locked
			}
			if keyLoaded, ok := ds["key_loaded"].(bool); ok && keyLoaded {
				summary["key_loaded"] = keyLoaded
			}
		}
	}

	// Children count (useful for understanding hierarchy)
	if children, ok := ds["children"].([]interface{}); ok {
		summary["children_count"] = len(children)
	}

	return summary
}

// sortDatasets sorts a slice of simplified datasets by the specified field
func sortDatasets(datasets []map[string]interface{}, orderBy string) {
	sort.Slice(datasets, func(i, j int) bool {
		switch orderBy {
		case "used":
			// Sort by used_bytes descending (largest first)
			iUsed, iOk := datasets[i]["used_bytes"].(float64)
			jUsed, jOk := datasets[j]["used_bytes"].(float64)
			if iOk && jOk {
				return iUsed > jUsed
			}
			return false
		case "available":
			// Sort by available_bytes descending (most available first)
			iAvail, iOk := datasets[i]["available_bytes"].(float64)
			jAvail, jOk := datasets[j]["available_bytes"].(float64)
			if iOk && jOk {
				return iAvail > jAvail
			}
			return false
		case "name":
			// Sort by name alphabetically
			iName, iOk := datasets[i]["name"].(string)
			jName, jOk := datasets[j]["name"].(string)
			if iOk && jOk {
				return iName < jName
			}
			return false
		default:
			// Default to name
			iName, iOk := datasets[i]["name"].(string)
			jName, jOk := datasets[j]["name"].(string)
			if iOk && jOk {
				return iName < jName
			}
			return false
		}
	})
}

func handleQueryShares(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	shareType := "all"
	if st, ok := args["share_type"].(string); ok && st != "" {
		shareType = st
	}

	response := make(map[string]interface{})

	// Query SMB shares
	if shareType == "smb" || shareType == "all" {
		result, err := client.CallContext(ctx, "sharing.smb.query")
		if err != nil {
			return "", fmt.Errorf("failed to query SMB shares: %w", err)
		}

		var smbShares []map[string]interface{}
		if err := json.Unmarshal(result, &smbShares); err != nil {
			return "", fmt.Errorf("failed to parse SMB shares: %w", err)
		}
		response["smb_shares"] = smbShares
	}

	// Query NFS shares
	if shareType == "nfs" || shareType == "all" {
		result, err := client.CallContext(ctx, "sharing.nfs.query")
		if err != nil {
			return "", fmt.Errorf("failed to query NFS shares: %w", err)
		}

		var nfsShares []map[string]interface{}
		if err := json.Unmarshal(result, &nfsShares); err != nil {
			return "", fmt.Errorf("failed to parse NFS shares: %w", err)
		}
		response["nfs_shares"] = nfsShares
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

func handleQuerySnapshots(ctx context.Context, client *truenas.Client, args map[string]interface{}) (string, error) {
	// Build query filters - initialize as empty array, not nil (API expects [] not null)
	filters := []interface{}{}
	if dataset, ok := args["dataset"].(string); ok && dataset != "" {
		filters = append(filters, []interface{}{"dataset", "=", dataset})
	}
	if pool, ok := args["pool"].(string); ok && pool != "" {
		filters = append(filters, []interface{}{"pool", "=", pool})
	}

	// Options parameter (required by API even if empty)
	options := map[string]interface{}{}

	result, err := client.CallContext(ctx, "pool.snapshot.query", filters, options)
	if err != nil {
		return "", err
	}

	var snapshots []map[string]interface{}
	if err := json.Unmarshal(result, &snapshots); err != nil {
		return "", fmt.Errorf("failed to parse snapshots: %w", err)
	}

	// Simplify response
	simplified := make([]map[string]interface{}, 0, len(snapshots))
	for _, snap := range snapshots {
		summary := simplifySnapshot(snap)
		simplified = append(simplified, summary)
	}

	// Filter by holds_only if requested
	if holdsOnly, ok := args["holds_only"].(bool); ok && holdsOnly {
		filtered := make([]map[string]interface{}, 0)
		for _, snap := range simplified {
			if holdsCount, ok := snap["holds_count"].(int); ok && holdsCount > 0 {
				filtered = append(filtered, snap)
			}
		}
		simplified = filtered
	}

	// Sort snapshots
	orderBy := "name" // default to sorting by snapshot name descending
	if order, ok := args["order_by"].(string); ok && order != "" {
		orderBy = order
	}
	sortSnapshots(simplified, orderBy)

	// Apply limit (default to 50 for manageable response size)
	limit := 50
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	totalSnapshots := len(simplified)
	if len(simplified) > limit {
		simplified = simplified[:limit]
	}

	// Add metadata wrapper
	response := map[string]interface{}{
		"snapshots":       simplified,
		"snapshot_count":  len(simplified),
		"total_snapshots": totalSnapshots,
	}
	if dataset, ok := args["dataset"].(string); ok && dataset != "" {
		response["dataset_filter"] = dataset
	}
	if pool, ok := args["pool"].(string); ok && pool != "" {
		response["pool_filter"] = pool
	}
	if holdsOnly, ok := args["holds_only"].(bool); ok && holdsOnly {
		response["holds_filter"] = "only snapshots with holds"
	}
	if len(simplified) < totalSnapshots {
		response["note"] = fmt.Sprintf("Showing %d of %d snapshots (limited)", len(simplified), totalSnapshots)
	}

	formatted, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	return string(formatted), nil
}

// simplifySnapshot extracts the most relevant fields from a raw snapshot object
func simplifySnapshot(snap map[string]interface{}) map[string]interface{} {
	summary := map[string]interface{}{
		"snapshot_name": snap["snapshot_name"],
		"dataset":       snap["dataset"],
		"pool":          snap["pool"],
	}

	// Parse creation date from snapshot name if it matches pattern
	if snapName, ok := snap["snapshot_name"].(string); ok {
		if parsedDate := parseSnapshotDate(snapName); parsedDate != "" {
			summary["created_date"] = parsedDate
		}
	}

	// Add createtxg for reference
	if txg, ok := snap["createtxg"].(string); ok {
		summary["createtxg"] = txg
	}

	// Count holds and extract names
	if holds, ok := snap["holds"].(map[string]interface{}); ok {
		if len(holds) > 0 {
			summary["holds_count"] = len(holds)
			holdNames := make([]string, 0, len(holds))
			for name := range holds {
				holdNames = append(holdNames, name)
			}
			summary["holds"] = holdNames
		}
	}

	// Include full snapshot ID for reference
	if id, ok := snap["id"].(string); ok {
		summary["full_name"] = id
	}

	return summary
}

// parseSnapshotDate attempts to extract date information from snapshot names
func parseSnapshotDate(name string) string {
	// Common patterns used by automatic snapshot tasks
	patterns := []struct {
		layout string
		prefix string
	}{
		{"2006-01-02_15-04", "auto-"},    // auto-YYYY-MM-DD_HH-MM
		{"2006-01-02", "auto-"},          // auto-YYYY-MM-DD
		{"2006-01-02_15-04", ""},         // YYYY-MM-DD_HH-MM
		{"2006-01-02", ""},               // YYYY-MM-DD
		{"20060102-1504", "auto-"},       // auto-YYYYMMDD-HHMM
		{"20060102", "auto-"},            // auto-YYYYMMDD
		{"2006-01-02_15-04-05", "auto-"}, // auto-YYYY-MM-DD_HH-MM-SS
		{"2006-01-02_1504", ""},          // YYYY-MM-DD_HHMM
	}

	for _, p := range patterns {
		// Try to extract date substring
		dateStr := name
		if p.prefix != "" && strings.HasPrefix(name, p.prefix) {
			dateStr = strings.TrimPrefix(name, p.prefix)
		}

		// Try parsing with this layout
		if t, err := time.Parse(p.layout, dateStr); err == nil {
			return t.Format("2006-01-02 15:04")
		}

		// Also try just the first part before any underscore
		if idx := strings.Index(dateStr, "_"); idx > 0 {
			if t, err := time.Parse("2006-01-02", dateStr[:idx]); err == nil {
				return t.Format("2006-01-02")
			}
		}
	}

	return "" // No date found
}

// sortSnapshots sorts a slice of simplified snapshots by the specified field
func sortSnapshots(snapshots []map[string]interface{}, orderBy string) {
	sort.Slice(snapshots, func(i, j int) bool {
		switch orderBy {
		case "name":
			// Sort by snapshot_name descending (newest automatic snapshots first)
			iName, iOk := snapshots[i]["snapshot_name"].(string)
			jName, jOk := snapshots[j]["snapshot_name"].(string)
			if iOk && jOk {
				return iName > jName // Descending
			}
			return false
		case "dataset":
			// Sort by dataset path alphabetically ascending
			iDataset, iOk := snapshots[i]["dataset"].(string)
			jDataset, jOk := snapshots[j]["dataset"].(string)
			if iOk && jOk {
				return iDataset < jDataset
			}
			return false
		case "created":
			// Sort by parsed created_date descending, fallback to name
			iCreated, iOk := snapshots[i]["created_date"].(string)
			jCreated, jOk := snapshots[j]["created_date"].(string)
			if iOk && jOk {
				return iCreated > jCreated
			}
			// Fallback to name comparison
			iName, iOk := snapshots[i]["snapshot_name"].(string)
			jName, jOk := snapshots[j]["snapshot_name"].(string)
			if iOk && jOk {
				return iName > jName
			}
			return false
		default:
			// Default to name descending
			iName, iOk := snapshots[i]["snapshot_name"].(string)
			jName, jOk := snapshots[j]["snapshot_name"].(string)
			if iOk && jOk {
				return iName > jName
			}
			return false
		}
	})
}
