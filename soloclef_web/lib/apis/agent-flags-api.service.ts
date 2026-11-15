import { z } from "zod";
import { baseApiService } from "./base-api.service";

// The three flags the API answers (soloclef_api/internal/agentflags): the ones
// this app reads, each true only for something the server really does. There
// is no permission flag: the "ask first" choices are always listed, because
// the chat always honours them.
const AgentFeatureFlagsSchema = z.object({
	disable_new_agent_stack: z.boolean(),
	enable_action_log: z.boolean(),
	enable_revert_route: z.boolean(),
});

export type AgentFeatureFlags = z.infer<typeof AgentFeatureFlagsSchema>;

class AgentFlagsApiService {
	get = async (): Promise<AgentFeatureFlags> => {
		return baseApiService.get(`/api/v1/agent/flags`, AgentFeatureFlagsSchema);
	};
}

export const agentFlagsApiService = new AgentFlagsApiService();
