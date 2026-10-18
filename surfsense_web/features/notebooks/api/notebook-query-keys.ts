export const notebookKeys = {
	list: (workspaceId: number) => ["notebooks", workspaceId] as const,
	/** Its own root: invalidating a notebook list must not refetch every search. */
	search: (workspaceId: number, query: string) => ["notebook-search", workspaceId, query] as const,
	detail: (workspaceId: number, notebookId: number) =>
		["notebooks", workspaceId, notebookId] as const,
	sources: (workspaceId: number, notebookId: number) =>
		["notebooks", workspaceId, notebookId, "sources"] as const,
	researchSources: (workspaceId: number, notebookId: number) =>
		["notebooks", workspaceId, notebookId, "research-sources"] as const,
	guide: (workspaceId: number, notebookId: number, sourceId: number) =>
		["notebooks", workspaceId, notebookId, "guide", sourceId] as const,
	notes: (workspaceId: number, notebookId: number) =>
		["notebooks", workspaceId, notebookId, "notes"] as const,
	/** One note, as the Notes page opens it; under `notes`, so refreshing those refreshes it. */
	note: (workspaceId: number, notebookId: number, noteId: number) =>
		["notebooks", workspaceId, notebookId, "notes", noteId] as const,
	threads: (workspaceId: number, notebookId: number) =>
		["notebooks", workspaceId, notebookId, "threads"] as const,
	outputs: (workspaceId: number, notebookId: number) =>
		["notebooks", workspaceId, notebookId, "outputs"] as const,
	suggestions: (
		workspaceId: number,
		notebookId: number,
		kind: string,
		variant = "",
		language = ""
	) => ["notebooks", workspaceId, notebookId, "suggestions", kind, variant, language] as const,
	followups: (workspaceId: number, notebookId: number, messageId: string) =>
		["notebooks", workspaceId, notebookId, "followups", messageId] as const,
	reportView: (workspaceId: number, notebookId: number, outputId: number) =>
		["notebooks", workspaceId, notebookId, "report-view", outputId] as const,
	outputView: (workspaceId: number, notebookId: number, outputId: number) =>
		["notebooks", workspaceId, notebookId, "output-view", outputId] as const,
	reportTemplates: (workspaceId: number) => ["notebook-report-templates", workspaceId] as const,
};
