package tools

import (
	"github.com/truenas/truenas-mcp/mcp"
)

// Directory tools: directory service configuration.
//
// Split out of the former single-file registry so that each domain's tool
// definitions sit next to the handlers that implement them.

func (r *Registry) registerDirectoryTools() {
	// Directory Services
	r.tools["get_directory_service_status"] = Tool{
		Definition: mcp.Tool{
			Name:        "get_directory_service_status",
			Description: "Get current directory service status and health. Returns service type (ACTIVEDIRECTORY, IPA, LDAP), status (DISABLED, HEALTHY, FAULTED, JOINING, LEAVING), and error messages if any. Use for quick health checks.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleGetDirectoryServiceStatus,
	}

	r.tools["query_directory_services"] = Tool{
		Definition: mcp.Tool{
			Name:        "query_directory_services",
			Description: "Query full directory service configuration. Returns service type, enabled status, credentials (masked for security), and service-specific settings. All passwords and keytabs are masked in output.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleQueryDirectoryServices,
	}

	r.tools["list_directory_certificates"] = Tool{
		Definition: mcp.Tool{
			Name:        "list_directory_certificates",
			Description: "List available certificates for LDAP MTLS authentication. Returns certificate IDs and names that can be used with LDAP_MTLS credential type.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleListDirectoryCertificates,
	}

	r.tools["refresh_directory_cache"] = Tool{
		Definition: mcp.Tool{
			Name:        "refresh_directory_cache",
			Description: "Refresh cached user and group data from the directory service. Use after making changes in Active Directory, LDAP, or IPA that need to be reflected immediately in TrueNAS.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: handleRefreshDirectoryCache,
	}

	r.tools["configure_directory_service"] = Tool{
		Definition: mcp.Tool{
			Name: "configure_directory_service",
			Description: `Configure and join a directory service (Active Directory, LDAP, or IPA). Setting enable=true joins the domain automatically.

**Service Types:**
- ACTIVEDIRECTORY: Microsoft Active Directory integration
- LDAP: Generic LDAP server (OpenLDAP, etc.)
- IPA: FreeIPA / Red Hat Identity Management

**Credential Types by Service:**

Active Directory:
- KERBEROS_USER: {type: "KERBEROS_USER", username: "admin", password: "pass"}
- KERBEROS_PRINCIPAL: {type: "KERBEROS_PRINCIPAL", principal: "host/truenas", keytab: "..."}

LDAP:
- LDAP_PLAIN: {type: "LDAP_PLAIN", binddn: "cn=admin,dc=example,dc=com", bindpw: "pass"}
- LDAP_ANONYMOUS: {type: "LDAP_ANONYMOUS"}
- LDAP_MTLS: {type: "LDAP_MTLS", certificate_id: 123}
- KERBEROS_USER or KERBEROS_PRINCIPAL (same as Active Directory)

IPA:
- KERBEROS_USER or KERBEROS_PRINCIPAL (same as Active Directory)

**Configuration Object (service-specific):**
For Active Directory: {hostname: "truenas-nyc", domain: "corp.example.com", ...}
For LDAP: {hostname: "ldap.example.com", port: 389, ...}
For IPA: {hostname: "ipa.example.com", domain: "example.com", ...}

**Security:**
- Credentials are stored in TrueNAS configuration
- Use Kerberos principals with keytabs instead of passwords for production
- Dry-run shows credential requirements without exposing values

**Returns:** task_id for tracking long-running domain join operation (2-10 minutes typical)`,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"service_type": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"ACTIVEDIRECTORY", "LDAP", "IPA"},
						"description": "Directory service type",
					},
					"enable": map[string]interface{}{
						"type":        "boolean",
						"description": "Enable service (true to join domain, false to disable)",
					},
					"credential": map[string]interface{}{
						"type":        "object",
						"description": "Credential object with 'type' field and credential-specific fields (see tool description)",
					},
					"configuration": map[string]interface{}{
						"type":        "object",
						"description": "Service-specific configuration (domain, hostname, etc.)",
					},
					"enable_account_cache": map[string]interface{}{
						"type":        "boolean",
						"description": "Cache user/group lists (default: true)",
						"default":     true,
					},
					"enable_dns_updates": map[string]interface{}{
						"type":        "boolean",
						"description": "Auto DNS updates via nsupdate (default: true)",
						"default":     true,
					},
					"timeout": map[string]interface{}{
						"type":        "integer",
						"description": "DNS query and LDAP request timeout in seconds (5-60, default: 10)",
						"default":     10,
					},
					"kerberos_realm": map[string]interface{}{
						"type":        "string",
						"description": "Kerberos realm for authentication (optional)",
					},
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview configuration without executing (default: false)",
						"default":     false,
					},
				},
				"required": []string{"service_type", "enable", "credential"},
			},
		},
		Handler: r.handleConfigureDirectoryServiceWithDryRun,
	}

	r.tools["leave_directory_service"] = Tool{
		Definition: mcp.Tool{
			Name: "leave_directory_service",
			Description: `Disconnect from directory service and leave the domain.

**WARNING:** This is a destructive operation:
- Removes TrueNAS from the domain
- Deletes computer account (if possible)
- Clears all cached user/group data
- All domain user authentication will stop working
- SMB/NFS shares configured with domain users will become inaccessible

**Alternative:** Use configure_directory_service with enable=false for temporary disable without leaving the domain.

**Returns:** task_id for tracking the leave operation (30 seconds to 5 minutes typical)`,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"dry_run": map[string]interface{}{
						"type":        "boolean",
						"description": "Preview operation without executing (default: false, STRONGLY RECOMMENDED to use dry_run first)",
						"default":     false,
					},
				},
			},
		},
		Handler: r.handleLeaveDirectoryServiceWithDryRun,
	}
}
