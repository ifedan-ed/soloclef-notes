import { z } from "zod";

/**
 * An MCP server's settings, as the API reads and writes them.
 *
 * Remote servers only: Streamable HTTP or SSE. Local (stdio) servers are
 * refused by the server, and the old "http" spelling of Streamable HTTP is
 * no longer accepted, so each transport has one name.
 *
 * Every secret is a field of its own (the API key, the bearer token, the
 * OAuth client secret, each extra header's value) because the server
 * encrypts each one at rest and answers it masked: "••••" or "••••" plus the
 * last four characters. Sending a masked value back unchanged keeps the
 * stored secret, as long as the server's host is unchanged.
 */
export const mcpTransport = z.enum(["streamable-http", "sse"]);
export type MCPTransport = z.infer<typeof mcpTransport>;

/** How the server is signed in to, in a request. */
export const mcpAuthRequest = z.discriminatedUnion("type", [
	z.object({ type: z.literal("none") }),
	z.object({ type: z.literal("header"), header_name: z.string(), value: z.string() }),
	z.object({ type: z.literal("bearer"), token: z.string() }),
	z.object({
		type: z.literal("oauth"),
		client_id: z.string().optional(),
		client_secret: z.string().optional(),
	}),
]);
export type MCPAuthRequest = z.infer<typeof mcpAuthRequest>;

/** How the server is signed in to, as answered: secrets masked, OAuth's state said. */
export const mcpAuthRead = z.discriminatedUnion("type", [
	z.object({ type: z.literal("none") }),
	z.object({ type: z.literal("header"), header_name: z.string(), value: z.string() }),
	z.object({ type: z.literal("bearer"), token: z.string() }),
	z.object({
		type: z.literal("oauth"),
		client_id: z.string().nullable(),
		client_secret: z.string().nullable(),
		dynamic_registration: z.boolean(),
		authorization_server: z.string().nullable(),
		authorized: z.boolean(),
	}),
]);
export type MCPAuthRead = z.infer<typeof mcpAuthRead>;

/** The settings a create, an update or a test sends. */
export const mcpServerConfig = z.object({
	url: z.string(),
	transport: mcpTransport,
	auth: mcpAuthRequest,
	headers: z.record(z.string(), z.string()),
});
export type MCPServerConfig = z.infer<typeof mcpServerConfig>;

/** The settings an answer carries, masked. */
export const mcpServerConfigRead = z.object({
	url: z.string(),
	transport: mcpTransport,
	auth: mcpAuthRead,
	headers: z.record(z.string(), z.string()).default({}),
});
export type MCPServerConfigRead = z.infer<typeof mcpServerConfigRead>;

export const mcpConnectorCreate = z.object({
	name: z.string().min(1, "Connector name is required"),
	server_config: mcpServerConfig,
});

export const mcpConnectorUpdate = z.object({
	name: z.string().min(1).optional(),
	server_config: mcpServerConfig.optional(),
});

export const mcpConnectorRead = z.object({
	id: z.number(),
	name: z.string(),
	connector_type: z.literal("MCP_CONNECTOR"),
	server_config: mcpServerConfigRead,
	enabled: z.boolean(),
	user_id: z.string(),
	created_at: z.string(),
	updated_at: z.string(),
});

export const createMCPConnectorRequest = z.object({
	data: mcpConnectorCreate,
});

export const updateMCPConnectorRequest = z.object({
	id: z.number(),
	data: mcpConnectorUpdate,
});

export type MCPConnectorCreate = z.infer<typeof mcpConnectorCreate>;
export type MCPConnectorUpdate = z.infer<typeof mcpConnectorUpdate>;
export type MCPConnectorRead = z.infer<typeof mcpConnectorRead>;
export type CreateMCPConnectorRequest = z.infer<typeof createMCPConnectorRequest>;
export type UpdateMCPConnectorRequest = z.infer<typeof updateMCPConnectorRequest>;

/** One tool a server lists. */
export type MCPToolDefinition = {
	name: string;
	description: string;
	input_schema: Record<string, unknown>;
};

/** A connection test's answer: every failure is the same sentence (the reason is logged). */
export type MCPTestConnectionResponse = {
	status: "success" | "error";
	message: string;
	tools: MCPToolDefinition[];
};

/** The body of a connection test: the settings, and the saved server whose masked secrets stand in. */
export type MCPTestConnectionRequest = MCPServerConfig & { connector_id?: number };

/** Who may add a server, under which rule, and whether OAuth sign-in is set up here. */
export type MCPPolicy = {
	can_add: boolean;
	allow_list_applies: boolean;
	oauth_available: boolean;
};

/** What OAuth discovery found for a server address. */
export type MCPOAuthDiscovery =
	| { status: "success"; authorization_server: string; dynamic_registration: boolean }
	| { status: "error"; message: string };
