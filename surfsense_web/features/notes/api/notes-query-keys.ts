export const notesKeys = {
	/** Every Notes page query of a workspace: invalidated after any note change. */
	all: (workspaceId: number) => ["workspace-notes", workspaceId] as const,
	list: (workspaceId: number, query: string) => ["workspace-notes", workspaceId, query] as const,
	document: (workspaceId: number, documentId: number) =>
		["workspace-notes", workspaceId, "document", documentId] as const,
};
