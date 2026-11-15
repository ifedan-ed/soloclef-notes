import type { AgentTool } from "@/lib/apis/agent-permissions-api.service";

/** One heading of the settings page and the tools under it. */
export type ToolGroup = { heading: string; tools: AgentTool[] };

/**
 * A tool's identity on the page: an app's tool is told apart from the chat's
 * own tool of the same name (and from another app's) by its connection.
 */
export function toolKey(tool: Pick<AgentTool, "tool" | "connector_id">): string {
	return `${tool.connector_id ?? "chat"}:${tool.tool}`;
}

/**
 * The server's flat list as the page's sections, in the server's order: a
 * heading appears where its first tool does, so the order is decided in one
 * place (the API's catalogue) and a new heading needs nothing here. Two apps
 * of one name stay two sections, because each is its own connection.
 */
export function groupTools(tools: AgentTool[]): ToolGroup[] {
	const groups: ToolGroup[] = [];
	const byKey = new Map<string, ToolGroup>();
	for (const tool of tools) {
		const key = `${tool.connector_id ?? "chat"}:${tool.group}`;
		let group = byKey.get(key);
		if (!group) {
			group = { heading: tool.group, tools: [] };
			byKey.set(key, group);
			groups.push(group);
		}
		group.tools.push(tool);
	}
	return groups;
}

/** The list with one tool's choice changed, for showing a save before it lands. */
export function withChoice(
	tools: AgentTool[],
	choice: Pick<AgentTool, "tool" | "connector_id" | "ask_first">
): AgentTool[] {
	const key = toolKey(choice);
	return tools.map((tool) =>
		toolKey(tool) === key ? { ...tool, ask_first: choice.ask_first } : tool
	);
}
