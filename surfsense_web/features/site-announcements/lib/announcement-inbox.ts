/**
 * How an announcement behaves in the inbox: whether it still shows, and where
 * opening it goes.
 */

import { isSafeHref } from "./markdown-lite";

export const SITE_ANNOUNCEMENT_TYPE = "site_announcement";

interface InboxLike {
	type: string;
	metadata?: unknown;
}

function metadataOf(item: InboxLike): Record<string, unknown> {
	return item.metadata && typeof item.metadata === "object"
		? (item.metadata as Record<string, unknown>)
		: {};
}

/** An announcement past its expiry; every other notification never expires. */
export function isExpiredAnnouncement(item: InboxLike, now: Date = new Date()): boolean {
	if (item.type !== SITE_ANNOUNCEMENT_TYPE) return false;
	const expiresAt = metadataOf(item).expires_at;
	if (typeof expiresAt !== "string" || !expiresAt) return false;
	const when = new Date(expiresAt);
	return !Number.isNaN(when.getTime()) && when.getTime() <= now.getTime();
}

export type AnnouncementTarget = { kind: "app"; href: string } | { kind: "web"; href: string };

/** Where opening the announcement goes: a page in the app, a new tab, or nowhere. */
export function announcementTarget(item: InboxLike): AnnouncementTarget | null {
	if (item.type !== SITE_ANNOUNCEMENT_TYPE) return null;
	const link = metadataOf(item).link;
	if (typeof link !== "string" || !isSafeHref(link)) return null;
	return /^\/(?![\/\\])/.test(link) ? { kind: "app", href: link } : { kind: "web", href: link };
}
