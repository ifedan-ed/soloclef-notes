/**
 * How the composer's "Manage Tools" list is grouped.
 *
 * The list is GET /api/v1/agent/tools, which is the account's own: the
 * chat's own tools, then the tools of each thing the account connected (its
 * mailboxes, Google Drive, Nextcloud, each MCP server), every one of them a
 * tool its chat turns are offered (soloclef_assistant/assembly, Menu). A
 * connection's tools come with the connection's name (`group`) and kind
 * (`connector`), so the menu shows each connection as one row with one
 * switch for all of its tools, and nothing here has to know which tools a
 * connection has or whether it is connected: the server listed it, so it is.
 *
 * The chat's own tools are grouped here, by name, because their grouping is
 * the menu's wording, not the server's. A name not placed in a group goes
 * under "Other", so a tool the server adds is never hidden.
 *
 * The phone groups the same way (soloclef_mobile src/lib/chat/toolGroups.ts);
 * its parity test reads OWN_TOOL_GROUPS from this file.
 */

/** One entry of GET /api/v1/agent/tools. */
export interface MenuToolEntry {
	name: string;
	description: string;
	enabled_by_default: boolean;
	/** The connection's name, as the menu heads it; absent for the chat's own. */
	group?: string;
	/** The connection's kind: mail, google_drive, nextcloud or mcp. */
	connector?: string;
}

/** The chat's own tools, in the menu's order (the Go assembly's switchable list). */
export const OWN_TOOL_GROUPS: readonly { label: string; tools: readonly string[] }[] = [
	{ label: "Your sources", tools: ["search_knowledge_base", "add_sources"] },
	{
		label: "Research",
		tools: [
			"exa_web_search",
			"tavily_web_search",
			"pubmed_search",
			"arxiv_search",
			"preprint_search",
			"web_crawl",
		],
	},
	{ label: "Memory", tools: ["update_memory"] },
];

export interface ToolMenuGroup<T extends MenuToolEntry = MenuToolEntry> {
	label: string;
	tools: T[];
	/** Set for a connection's group: its kind, which picks the icon. */
	connector?: string;
}

export interface ToolMenu<T extends MenuToolEntry = MenuToolEntry> {
	/** The chat's own tools, by OWN_TOOL_GROUPS. */
	own: ToolMenuGroup<T>[];
	/** One group per connection, in the server's order. */
	connections: ToolMenuGroup<T>[];
	/** The chat's own tools no group names. */
	other?: ToolMenuGroup<T>;
}

/** Group the server's list for the menu. */
export function groupMenuTools<T extends MenuToolEntry>(tools: readonly T[]): ToolMenu<T> {
	const ownTools = tools.filter((tool) => !tool.group);
	const byName = new Map(ownTools.map((tool) => [tool.name, tool]));
	const placed = new Set<string>();
	const own: ToolMenuGroup<T>[] = [];
	for (const group of OWN_TOOL_GROUPS) {
		const matched = group.tools.flatMap((name) => {
			const tool = byName.get(name);
			if (!tool) return [];
			placed.add(name);
			return [tool];
		});
		if (matched.length > 0) own.push({ label: group.label, tools: matched });
	}

	// Two connections of one kind can share a name (two MCP servers both
	// called "Notes"), so a group is its kind and its name together.
	const connections: ToolMenuGroup<T>[] = [];
	const byKey = new Map<string, ToolMenuGroup<T>>();
	for (const tool of tools) {
		if (!tool.group) continue;
		const key = `${tool.connector ?? ""}\u0000${tool.group}`;
		let group = byKey.get(key);
		if (!group) {
			group = { label: tool.group, tools: [], connector: tool.connector };
			byKey.set(key, group);
			connections.push(group);
		}
		group.tools.push(tool);
	}

	const rest = ownTools.filter((tool) => !placed.has(tool.name));
	return { own, connections, other: rest.length > 0 ? { label: "Other", tools: rest } : undefined };
}

/** A stable key for a connection's group, for React keys and an open submenu. */
export function menuGroupKey(group: { label: string; connector?: string }): string {
	return `${group.connector ?? "own"}:${group.label}`;
}
