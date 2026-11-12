import { z } from "zod";
import { baseApiService } from "./base-api.service";

// The five flags the API answers (soloclef_api/internal/agentflags): the ones
// this app reads, each true only for something the server really does.
const AgentFeatureFlagsSchema = z.object({
	disable_new_agent_stack: z.boolean(),
	enable_permission: z.boolean(),
	enable_action_log: z.boolean(),
	enable_revert_route: z.boolean(),
	enable_desktop_local_filesystem: z.boolean(),
});

export type AgentFeatureFlags = z.infer<typeof AgentFeatureFlagsSchema>;

class AgentFlagsApiService {
	get = async (): Promise<AgentFeatureFlags> => {
		return baseApiService.get(`/api/v1/agent/flags`, AgentFeatureFlagsSchema);
	};
}

export const agentFlagsApiService = new AgentFlagsApiService();
