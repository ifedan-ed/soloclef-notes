import { z } from "zod";
import { baseApiService } from "./base-api.service";

/**
 * Which tools the assistant asks about before it uses them
 * (soloclef_api/internal/agentrules).
 *
 * Every tool runs without asking unless the person marks it "ask first".
 * The list is every tool the chat can use: its own, under a heading, and
 * each connected app's, under the app's name and with the app's connection.
 */
const AgentToolSchema = z.object({
	/** The tool's name: the chat's for its own tools, the server's for an app's. */
	tool: z.string(),
	/** The heading it is listed under: a kind of tool, or the app's name. */
	group: z.string(),
	/** The connected app's connection; null for the chat's own tools. */
	connector_id: z.number().nullable(),
	ask_first: z.boolean(),
});

export type AgentTool = z.infer<typeof AgentToolSchema>;

const AgentToolListSchema = z.array(AgentToolSchema);

/** One tool's choice, as the server takes it. */
export type AskFirstChoice = Pick<AgentTool, "tool" | "connector_id" | "ask_first">;

class AgentPermissionsApiService {
	// The server takes the account from the session, so no route names whose
	// tools they are.
	list = async (): Promise<AgentTool[]> => {
		return baseApiService.get(`/api/v1/agent/permissions/tools`, AgentToolListSchema);
	};

	/** Mark a tool "ask first", or take the mark off; it applies from the very next call. */
	setAskFirst = async (choice: AskFirstChoice): Promise<void> => {
		await baseApiService.put(`/api/v1/agent/permissions/ask-first`, undefined, { body: choice });
	};
}

export const agentPermissionsApiService = new AgentPermissionsApiService();
