/**
 * Curated example chat prompts shown on the empty new-chat screen.
 *
 * These mirror the homepage hero's "use case" concept but with runnable chat
 * queries, grouped into a few broad categories. Bracketed slots like `[topic]`
 * are intentional: clicking a prompt prefills the composer so the user can fill
 * them in before sending.
 *
 * This is a module-scope constant so it is created once, not per render.
 */

export interface ChatExampleCategory {
	/** Stable id used as the Tabs value */
	id: string;
	/** Short, human-readable tab label */
	label: string;
	/** Runnable example queries for this category */
	prompts: string[];
}

export const CHAT_EXAMPLE_CATEGORIES: ChatExampleCategory[] = [
	{
		id: "research",
		label: "Research the Web",
		prompts: ["Research [topic] across the live web and give me a cited brief"],
	},
	{
		id: "automate",
		label: "Automate",
		prompts: ["Watch [url] daily and alert me on any change"],
	},
	{
		id: "tools",
		label: "Across your tools",
		prompts: [
			"Search my Google Drive and Nextcloud files for [topic]",
			"Save this research summary as a new file in my Google Drive",
			"Summarise the Nextcloud documents I imported about [topic]",
			"Use my connected MCP servers to look up [topic]",
		],
	},
];
