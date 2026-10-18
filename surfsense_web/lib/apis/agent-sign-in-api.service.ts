import { baseApiService } from "./base-api.service";

/** What claude.ai and ChatGPT need pasted to sign in (null when sign-in is off). */
export interface AgentSignInDetails {
	enabled: boolean;
	client_id: string | null;
	client_secret: string | null;
}

class AgentSignInApiService {
	getDetails = async () => {
		return baseApiService.get<AgentSignInDetails>("/api/v1/agent-sign-in");
	};
}

export const agentSignInApiService = new AgentSignInApiService();
