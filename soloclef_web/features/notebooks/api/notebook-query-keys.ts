export const notebookKeys = {
	list: () => ["notebooks"] as const,
	/** Its own root: invalidating a notebook list must not refetch every search. */
	search: (query: string) => ["notebook-search", query] as const,
	detail: (notebookId: number) => ["notebooks", notebookId] as const,
	sources: (notebookId: number) => ["notebooks", notebookId, "sources"] as const,
	researchSources: (notebookId: number) => ["notebooks", notebookId, "research-sources"] as const,
	guide: (notebookId: number, sourceId: number) =>
		["notebooks", notebookId, "guide", sourceId] as const,
	threads: (notebookId: number) => ["notebooks", notebookId, "threads"] as const,
	outputs: (notebookId: number) => ["notebooks", notebookId, "outputs"] as const,
	suggestions: (notebookId: number, kind: string, variant = "", language = "") =>
		["notebooks", notebookId, "suggestions", kind, variant, language] as const,
	followups: (notebookId: number, messageId: string) =>
		["notebooks", notebookId, "followups", messageId] as const,
	reportView: (notebookId: number, outputId: number) =>
		["notebooks", notebookId, "report-view", outputId] as const,
	outputView: (notebookId: number, outputId: number) =>
		["notebooks", notebookId, "output-view", outputId] as const,
	reportTemplates: () => ["notebook-report-templates"] as const,
};
