/**
 * Who provided an MCP server, as the backend stores it in the connector's
 * config (`server_origin`, app/mcp_security/server_origin.py), and the address
 * rule that follows. A server an administrator added (and every server from
 * before the split, which has no marker) is a site server: its host must be on
 * the allowed list. A server a workspace Owner or Editor added with their own
 * key may use any public https address. The backend enforces both.
 */
export type MCPServerOrigin = "site" | "member";

export function mcpServerOrigin(config: unknown): MCPServerOrigin {
	const value =
		config && typeof config === "object"
			? (config as Record<string, unknown>).server_origin
			: undefined;
	return value === "member" ? "member" : "site";
}

/** The address rule, in the words the add and edit forms show. */
export function mcpAddressRule(allowListApplies: boolean): string {
	return allowListApplies ? "on the allowed list" : "a public address";
}
