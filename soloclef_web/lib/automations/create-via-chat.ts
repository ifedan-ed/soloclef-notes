/**
 * "Create via chat": the Automations page opens one of the person's
 * notebooks with the start of an automation request already typed in its
 * chat, where the assistant drafts the automation onto a card.
 *
 * Why a notebook the person picks, newest first, and not one picked for
 * them: a notebook has the only chat there is, and the notebook it is
 * asked in is the one the assistant offers by default to keep results in
 * as notes, so it is worth one click to choose. The phone app's sheet does
 * the same (soloclef_mobile, CreateAutomationSheet.tsx), with the same
 * starting words.
 */

import { notebookPath } from "@/lib/dashboard-paths";

/** The notebook page's parameter that asks its chat to start a request. */
export const COMPOSE_PARAM = "compose";
/** Its one value: the start of an automation request. */
export const COMPOSE_AUTOMATION = "automation";
/** What the chat box starts with; the person finishes the sentence. */
export const AUTOMATION_STARTER = "Create an automation that ";

/** The notebook's page with its chat set to start an automation request. */
export function createViaChatPath(notebookId: number): string {
	return `${notebookPath(notebookId)}?${COMPOSE_PARAM}=${COMPOSE_AUTOMATION}`;
}

/** The newest notebooks first, by when each last changed, as the notebooks home lists them. */
export function newestFirst<T extends { id: number; updated_at: string }>(
	notebooks: readonly T[]
): T[] {
	return [...notebooks].sort(
		(a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at) || b.id - a.id
	);
}
