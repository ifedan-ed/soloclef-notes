/**
 * A link to one run of an automation: its page, with that run opened in the
 * run history (`?run=<id>`). The inbox's "automation finished" rows use it.
 * The page opens the run when it is among the runs the history shows; an
 * older one leaves the page as it is, with the history in view.
 */

import { automationPath } from "@/lib/dashboard-paths";

export const RUN_PARAM = "run";

export function automationRunPath(automationId: number, runId: number): string {
	return `${automationPath(automationId)}?${new URLSearchParams({ [RUN_PARAM]: String(runId) })}`;
}

/** The run a link asked to open, or null. */
export function readRunParam(search: string): number | null {
	const id = Number(new URLSearchParams(search).get(RUN_PARAM));
	return Number.isSafeInteger(id) && id > 0 ? id : null;
}
