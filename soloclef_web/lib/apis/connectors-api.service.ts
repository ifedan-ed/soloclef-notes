import {
	type CreateConnectorRequest,
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
	setConnectorEnabledRequest,
	setConnectorEnabledResponse,
	type UpdateConnectorRequest,
	updateConnectorRequest,
	updateConnectorResponse,
} from "@/contracts/types/connector.types";
import type {
	CreateMCPConnectorRequest,
	GetMCPConnectorsRequest,
	MCPConnectorRead,
	MCPServerConfig,
	MCPTestConnectionResponse,
	UpdateMCPConnectorRequest,
} from "@/contracts/types/mcp.types";
import { ValidationError } from "../error";
import { baseApiService } from "./base-api.service";

class ConnectorsApiService {
	/**
	 * Get all connectors for a workspace
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
			`/api/v1/search-source-connectors?${queryParams}`,
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

		const { data, queryParams } = parsedRequest.data;

		// Transform query params to be string values, filtering out undefined/null
		const transformedQueryParams = Object.fromEntries(
			Object.entries(queryParams)
				.filter(([_, v]) => v !== undefined && v !== null)
				.map(([k, v]) => {
					return [k, String(v)];
				})
		);

		const queryString = new URLSearchParams(transformedQueryParams).toString();

		return baseApiService.post(
			`/api/v1/search-source-connectors?${queryString}`,
			createConnectorResponse,
			{
				body: data,
			}
		);
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

		const { id, data, queryParams } = parsedRequest.data;
		const query = queryParams
			? `?${new URLSearchParams({ workspace_id: String(queryParams.workspace_id) })}`
			: "";

		return baseApiService.put(
			`/api/v1/search-source-connectors/${id}${query}`,
			updateConnectorResponse,
			{
				body: data,
			}
		);
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
	 * Switch an account connection on/off in a workspace. Owner only.
	 */
	setConnectorEnabled = async (request: SetConnectorEnabledRequest) => {
		const parsedRequest = setConnectorEnabledRequest.safeParse(request);

		if (!parsedRequest.success) {
			console.error("Invalid request:", parsedRequest.error);

			const errorMessage = parsedRequest.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		const { connectorId, workspaceId, enabled } = parsedRequest.data;
		const url = `/api/v1/search-source-connectors/${connectorId}/workspaces/${workspaceId}`;

		return enabled
			? baseApiService.post(url, setConnectorEnabledResponse)
			: baseApiService.delete(url, setConnectorEnabledResponse);
	};

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

		// Transform query params to be string values, filtering out undefined/null
		const transformedQueryParams = Object.fromEntries(
			Object.entries(queryParams)
				.filter(([_, v]) => v !== undefined && v !== null)
				.map(([k, v]) => {
					return [k, String(v)];
				})
		);

		const queryString = new URLSearchParams(transformedQueryParams).toString();

		return baseApiService.post(
			`/api/v1/search-source-connectors/${connector_id}/index?${queryString}`,
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
	 * Get all MCP connectors for a workspace
	 */
	getMCPConnectors = async (request: GetMCPConnectorsRequest) => {
		const { workspace_id } = request.queryParams;

		const queryString = new URLSearchParams({
			workspace_id: String(workspace_id),
		}).toString();

		return baseApiService.get<MCPConnectorRead[]>(`/api/v1/connectors/mcp?${queryString}`);
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
		const { data, queryParams } = request;

		const queryString = new URLSearchParams({
			workspace_id: String(queryParams.workspace_id),
		}).toString();

		return baseApiService.post<MCPConnectorRead>(
			`/api/v1/connectors/mcp?${queryString}`,
			undefined,
			{
				body: data,
			}
		);
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
	testMCPConnection = async (
		serverConfig: MCPServerConfig,
		workspaceId: number | string,
		connectorId?: number
	) => {
		const queryString = new URLSearchParams({ workspace_id: String(workspaceId) }).toString();
		return baseApiService.post<MCPTestConnectionResponse>(
			`/api/v1/connectors/mcp/test?${queryString}`,
			undefined,
			{
				// connector_id lets a saved server be tested with its masked keys.
				body:
					connectorId === undefined ? serverConfig : { ...serverConfig, connector_id: connectorId },
			}
		);
	};

	/**
	 * Whether the signed-in person may add MCP servers in this workspace (Owners
	 * and Editors), and whether the admin allow-list applies (an administrator's
	 * server is a shared site server; anyone else's is their own, on any public
	 * https address).
	 */
	getMCPPolicy = async (workspaceId?: number | string | null) => {
		const query =
			workspaceId === undefined || workspaceId === null
				? ""
				: `?${new URLSearchParams({ workspace_id: String(workspaceId) }).toString()}`;
		return baseApiService.get<{ can_add: boolean; allow_list_applies?: boolean }>(
			`/api/v1/connectors/mcp/policy${query}`
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
