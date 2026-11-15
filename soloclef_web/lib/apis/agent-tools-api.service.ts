import { z } from "zod";
import { baseApiService } from "./base-api.service";

// One tool the account's chat turns can be offered. A connection's tools
// (a mailbox's, Drive's, Nextcloud's, an MCP server's) carry the
// connection's name and kind, which the menu groups them by
// (features/composer-tools/tool-menu-groups.ts).
const AgentToolInfoSchema = z.object({
	name: z.string(),
	description: z.string(),
	enabled_by_default: z.boolean(),
	group: z.string().optional(),
	connector: z.string().optional(),
});

export type AgentToolInfo = z.infer<typeof AgentToolInfoSchema>;

const AgentToolsListSchema = z.array(AgentToolInfoSchema);

class AgentToolsApiService {
	async getTools(): Promise<AgentToolInfo[]> {
		return baseApiService.get("/api/v1/agent/tools", AgentToolsListSchema);
	}
}

export const agentToolsApiService = new AgentToolsApiService();
