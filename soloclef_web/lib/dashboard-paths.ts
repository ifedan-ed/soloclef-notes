/**
 * Where the signed-in pages live. Everything belongs to the account, so no
 * address carries anything but the page itself.
 */

/** The account's home: its notebooks. */
export const NOTEBOOKS_HOME = "/dashboard/notebooks";

/** Every note the account has. */
export const NOTES_PAGE = "/dashboard/notes";

/** The account's automations. */
export const AUTOMATIONS_PAGE = "/dashboard/automations";

/** A notebook's page; without a notebook, the notebooks home. Never names a chat. */
export function notebookPath(notebookId?: number | null): string {
	return notebookId && Number.isSafeInteger(notebookId) && notebookId > 0
		? `${NOTEBOOKS_HOME}/${notebookId}`
		: NOTEBOOKS_HOME;
}

/** The settings page for one section; without one, settings opens on its first. */
export function settingsPath(section?: string): string {
	return section ? `/dashboard/settings/${section}` : "/dashboard/settings";
}

/** One automation, or its editor. */
export function automationPath(automationId: number, edit = false): string {
	return `${AUTOMATIONS_PAGE}/${automationId}${edit ? "/edit" : ""}`;
}
