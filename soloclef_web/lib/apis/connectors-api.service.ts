import {
	type CreateConnectorRequest,
	connectorParser,
	createConnectorRequest,
	createConnectorResponse,
	type DeleteConnectorRequest,
	deleteConnectorRequest,
	deleteConnectorResponse,
	type GetConnectorRequest,
	type GetConnectorsRequest,
	getConnectorRequest,
	getConnectorResponse,
	getConnectorsRequest,
	getConnectorsResponse,
	type IndexConnectorRequest,
	indexConnectorRequest,
	indexConnectorResponse,
	type SetConnectorEnabledRequest,
	type SetConnectorParserRequest,
	setConnectorEnabledRequest,
	setConnectorEnabledResponse,
	setConnectorParserRequest,
	type UpdateConnectorRequest,
	updateConnectorRequest,
	updateConnectorResponse,
} from "@/contracts/types/connector.types";
import type {
	CreateMCPConnectorRequest,
	MCPConnectorRead,
	MCPServerConfig,
	MCPTestConnectionResponse,
	UpdateMCPConnectorRequest,
} from "@/contracts/types/mcp.types";
import { ValidationError } from "../error";
import { baseApiService } from "./base-api.service";

class ConnectorsApiService {
	/**
	 * Every connection of the signed-in account, of every type. Each item
	 * carries `enabled`, so no per-connection status request is needed.
	 */
	getConnectors = async (request: GetConnectorsRequest) => {
		const parsedRequest = getConnectorsRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		// Transform query params to be string values, filtering out undefined/null
		const transformedQueryParams = parsedRequest.data.queryParams
			? Object.fromEntries(
					Object.entries(parsedRequest.data.queryParams)
						.filter(([_, v]) => v !== undefined && v !== null)
						.map(([k, v]) => {
							return [k, String(v)];
						})
				)
			: undefined;

		const queryParams = transformedQueryParams
			? new URLSearchParams(transformedQueryParams).toString()
			: "";

		return baseApiService.get(
			`/api/v1/search-source-connectors${queryParams ? `?${queryParams}` : ""}`,
			getConnectorsResponse
		);
	};

	/**
	 * Get a single connector by ID
	 */
	getConnector = async (request: GetConnectorRequest) => {
		const parsedRequest = getConnectorRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		return baseApiService.get(
			`/api/v1/search-source-connectors/${request.id}`,
			getConnectorResponse
		);
	};

	/**
	 * Create a new connector
	 */
	createConnector = async (request: CreateConnectorRequest) => {
		const parsedRequest = createConnectorRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		return baseApiService.post(`/api/v1/search-source-connectors`, createConnectorResponse, {
			body: parsedRequest.data.data,
		});
	};

	/**
	 * Update an existing connector
	 */
	updateConnector = async (request: UpdateConnectorRequest) => {
		const parsedRequest = updateConnectorRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		const { id, data } = parsedRequest.data;

		return baseApiService.put(`/api/v1/search-source-connectors/${id}`, updateConnectorResponse, {
			body: data,
		});
	};

	/**
	 * Delete a connector
	 */
	deleteConnector = async (request: DeleteConnectorRequest) => {
		const parsedRequest = deleteConnectorRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		return baseApiService.delete(
			`/api/v1/search-source-connectors/${request.id}`,
			deleteConnectorResponse
		);
	};

	/**
	 * Switch a connection on or off: one PUT with the wanted state, so a
	 * repeated click is harmless; the answer is the connection.
	 */
	setConnectorEnabled = async (request: SetConnectorEnabledRequest) => {
		const parsedRequest = setConnectorEnabledRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		const { connectorId, enabled } = parsedRequest.data;

		return baseApiService.put(
			`/api/v1/search-source-connectors/${connectorId}/enabled`,
			setConnectorEnabledResponse,
			{ body: { enabled } }
		);
	};

	/**
	 * Choose which parser reads the files a connection brings in. Owner only.
	 * The body is validated here first so a value that is not one of the four
	 * parsers never leaves the browser.
	 */
	setConnectorParser = async (request: SetConnectorParserRequest) => {
		const parsedRequest = setConnectorParserRequest.safeParse(request);

		if (!parsedRequest.success) {
			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		const { connectorId, parser } = parsedRequest.data;
		return baseApiService.put(
			`/api/v1/search-source-connectors/${connectorId}/parser`,
			connectorParser,
			{ body: { parser } }
		);
	};

	/** The parser a connection's files are read by; null when none was chosen. Owner only. */
	getConnectorParser = async (connectorId: number) =>
		baseApiService.get(`/api/v1/search-source-connectors/${connectorId}/parser`, connectorParser);

	/**
	 * Index connector content
	 */
	indexConnector = async (request: IndexConnectorRequest) => {
		const parsedRequest = indexConnectorRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		const { connector_id, queryParams, body } = parsedRequest.data;

		// Only the dates that were given go into the query string.
		const dates = new URLSearchParams();
		if (queryParams?.start_date) dates.set("start_date", queryParams.start_date);
		if (queryParams?.end_date) dates.set("end_date", queryParams.end_date);
		const queryString = dates.toString();

		return baseApiService.post(
			`/api/v1/search-source-connectors/${connector_id}/index${queryString ? `?${queryString}` : ""}`,
			indexConnectorResponse,
			{
				body: body || {},
			}
		);
	};

	// =============================================================================
	// MCP Connector Methods
	// =============================================================================

	/**
	 * The signed-in account's MCP servers
	 */
	getMCPConnectors = async () => {
		return baseApiService.get<MCPConnectorRead[]>(`/api/v1/connectors/mcp`);
	};

	/**
	 * Get a single MCP connector by ID
	 */
	getMCPConnector = async (connectorId: number) => {
		return baseApiService.get<MCPConnectorRead>(`/api/v1/connectors/mcp/${connectorId}`);
	};

	/**
	 * Create a new MCP connector
	 */
	createMCPConnector = async (request: CreateMCPConnectorRequest) => {
		return baseApiService.post<MCPConnectorRead>(`/api/v1/connectors/mcp`, undefined, {
			body: request.data,
		});
	};

	/**
	 * Update an existing MCP connector
	 */
	updateMCPConnector = async (request: UpdateMCPConnectorRequest) => {
		const { id, data } = request;

		return baseApiService.put<MCPConnectorRead>(`/api/v1/connectors/mcp/${id}`, undefined, {
			body: data,
		});
	};

	/**
	 * Delete an MCP connector
	 */
	deleteMCPConnector = async (connectorId: number) => {
		return baseApiService.delete<void>(`/api/v1/connectors/mcp/${connectorId}`);
	};

	/**
	 * Test MCP server connection and retrieve available tools
	 */
	testMCPConnection = async (serverConfig: MCPServerConfig, connectorId?: number) => {
		return baseApiService.post<MCPTestConnectionResponse>(
			`/api/v1/connectors/mcp/test`,
			undefined,
			{
				// connector_id lets a saved server be tested with its masked keys.
				body:
					connectorId === undefined ? serverConfig : { ...serverConfig, connector_id: connectorId },
			}
		);
	};

	/**
	 * Whether the signed-in person may add MCP servers, and whether the admin
	 * allow-list applies (an administrator's server is a shared site server;
	 * anyone else's is their own, on any public https address).
	 */
	getMCPPolicy = async () => {
		return baseApiService.get<{ can_add: boolean; allow_list_applies?: boolean }>(
			`/api/v1/connectors/mcp/policy`
		);
	};

	/** Revoke a previously-trusted MCP tool so the next call asks again. */
	untrustMCPTool = async (connectorId: number, toolName: string): Promise<void> => {
		await baseApiService.post(`/api/v1/connectors/mcp/${connectorId}/untrust-tool`, undefined, {
			body: { tool_name: toolName },
		});
	};
}

export const connectorsApiService = new ConnectorsApiService();
