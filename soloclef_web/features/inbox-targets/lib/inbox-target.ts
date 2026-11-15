/**
 * Where opening an inbox item goes, for every type, and the "Try again" a
 * failed Studio output offers. One place decides it, so the dropdown, its
 * tests and any later surface agree. Every target is a page in this app:
 *
 * - a Studio output: its notebook, with Studio open;
 * - an automation run: the automation, with that run opened in its history;
 * - a mail draft: the page its link names, when it names one in the app;
 * - anything else (uploads, connector syncs): nowhere; opening it only marks
 *   it read, as before.
 *
 * One item opens a page outside the app instead: an App Store update's
 * listing (inboxStoreLink), in a new tab.
 */

import { studioHref } from "@/features/notebooks/lib/studio-link";
import { automationRunPath } from "@/lib/automations/run-link";

interface InboxLike {
	type: string;
	metadata?: unknown;
}

function metadataOf(item: InboxLike): Record<string, unknown> {
	return item.metadata && typeof item.metadata === "object"
		? (item.metadata as Record<string, unknown>)
		: {};
}

function positiveId(value: unknown): number | null {
	return typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : null;
}

/** A path inside this site ("/dashboard/…"), never "//host" or "/\\host", which a browser reads as another site. */
function isAppPath(value: unknown): value is string {
	return typeof value === "string" && /^\/(?![/\\])/.test(value);
}

/** The app page opening the item goes to, or null when opening it only marks it read. */
export function inboxTarget(item: InboxLike): string | null {
	const metadata = metadataOf(item);
	switch (item.type) {
		case "studio_output": {
			const notebookId = positiveId(metadata.notebook_id);
			return notebookId ? studioHref(notebookId) : null;
		}
		case "automation_run": {
			const automationId = positiveId(metadata.automation_id);
			const runId = positiveId(metadata.run_id);
			return automationId && runId ? automationRunPath(automationId, runId) : null;
		}
		case "mail_draft":
			return isAppPath(metadata.link) ? metadata.link : null;
		default:
			return null;
	}
}

/** Apple's App Store pages: the only outside address an inbox item may open. */
const APP_STORE_PAGE = /^https:\/\/apps\.apple\.com\/[^\s]*$/;

/**
 * The App Store listing an update item opens ("Soloclef 1.2.0 is out", which
 * the server writes for an account with an iPhone on an older version), or
 * null for every other item. Only an apps.apple.com address is taken, so a
 * row can never send the browser elsewhere.
 */
export function inboxStoreLink(item: InboxLike): string | null {
	if (item.type !== "app_update") return null;
	const link = metadataOf(item).store_url;
	return typeof link === "string" && APP_STORE_PAGE.test(link) ? link : null;
}

/**
 * The page that runs a failed Studio output again: its notebook's Studio,
 * told to retry that output once. Only for a failure the server marked
 * `retry: true` with both ids; null for everything else.
 */
export function retryHref(item: InboxLike): string | null {
	if (item.type !== "studio_output") return null;
	const metadata = metadataOf(item);
	if (metadata.status !== "failed" || metadata.retry !== true) return null;
	const notebookId = positiveId(metadata.notebook_id);
	const outputId = positiveId(metadata.output_id);
	return notebookId && outputId ? studioHref(notebookId, outputId) : null;
}
