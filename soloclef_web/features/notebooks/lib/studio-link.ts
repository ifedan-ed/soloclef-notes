/**
 * Links that open a notebook on its Studio (`?pane=studio`, the phone app's
 * spelling too) and, from a failed output's "Try again" in the inbox, run
 * that output again once (`&retry_output=<id>`). The page acts on them when
 * it opens and then drops them from the address, so a reload or a Back
 * neither reopens the pane nor runs the output a second time.
 */

import { notebookPath } from "@/lib/dashboard-paths";
import type { StudioOutput } from "../model/notebook";

export const PANE_PARAM = "pane";
export const RETRY_OUTPUT_PARAM = "retry_output";

/** The notebook with its Studio open; with `retryOutputId`, that output run again once. */
export function studioHref(notebookId: number, retryOutputId?: number): string {
	const params = new URLSearchParams({ [PANE_PARAM]: "studio" });
	if (retryOutputId !== undefined) params.set(RETRY_OUTPUT_PARAM, String(retryOutputId));
	return `${notebookPath(notebookId)}?${params}`;
}

export type StudioLink = { retryOutputId: number | null } | null;

/** What a Studio link asks for: null when the address has none. */
export function readStudioLink(search: string): StudioLink {
	const params = new URLSearchParams(search);
	const retry = Number(params.get(RETRY_OUTPUT_PARAM));
	const retryOutputId = Number.isSafeInteger(retry) && retry > 0 ? retry : null;
	if (params.get(PANE_PARAM) !== "studio" && retryOutputId === null) return null;
	return { retryOutputId };
}

/** `url` without a Studio link's parameters, once the page has acted on them. */
export function withoutStudioLinkParams(url: string): string {
	const next = new URL(url);
	next.searchParams.delete(PANE_PARAM);
	next.searchParams.delete(RETRY_OUTPUT_PARAM);
	return next.toString();
}

/** Outputs Studio offers to run again (its row menu's Retry). */
export function canRetry(output: Pick<StudioOutput, "status">): boolean {
	return output.status === "failed" || output.status === "cancelled";
}

export type RetryDecision = "wait" | "retry" | "gone";

/**
 * What to do with a link's output: wait for the outputs to load, run it
 * again (it is still failed or cancelled), or say it can't be: deleted, or
 * no longer one to retry. A link never runs an output that did not fail.
 */
export function retryDecision(
	outputs: readonly Pick<StudioOutput, "id" | "status">[] | undefined,
	outputId: number
): RetryDecision {
	if (!outputs) return "wait";
	const output = outputs.find((candidate) => candidate.id === outputId);
	return output && canRetry(output) ? "retry" : "gone";
}
