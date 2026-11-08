/** Where a workspace opens: the notebooks home. */
export function workspaceHomePath(workspaceId: number | string): string {
	return `/dashboard/${workspaceId}/notebooks`;
}

/** A notebook's page; without a notebook, the notebooks home. Never names a chat. */
export function notebookPath(workspaceId: number | string, notebookId?: number | null): string {
	const home = workspaceHomePath(workspaceId);
	return notebookId && Number.isSafeInteger(notebookId) && notebookId > 0
		? `${home}/${notebookId}`
		: home;
}
