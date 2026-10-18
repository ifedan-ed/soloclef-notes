/**
 * The compose form's draft and the rules it must meet before sending. They
 * mirror the server's, so the Send button only lights up for a draft the
 * server will accept.
 */

import type { AnnouncementBody } from "../api/announcements-admin-api";
import { isSafeHref } from "./markdown-lite";

export const TITLE_MAX = 120;
export const BODY_MAX = 2000;

export interface AnnouncementDraft {
	title: string;
	body: string;
	link: string;
	/** A datetime-local value ("2026-10-01T09:00"), in the admin's own time zone; "" for none. */
	expiresAt: string;
	audience: "all" | "workspace";
	workspaceId: number | null;
}

export const emptyDraft: AnnouncementDraft = {
	title: "",
	body: "",
	link: "",
	expiresAt: "",
	audience: "all",
	workspaceId: null,
};

export function draftProblems(draft: AnnouncementDraft, now: Date = new Date()): string[] {
	const problems: string[] = [];
	const title = draft.title.trim();
	if (!title) problems.push("Give the announcement a title.");
	else if (title.length > TITLE_MAX) problems.push(`Keep the title under ${TITLE_MAX} characters.`);
	const body = draft.body.trim();
	if (!body) problems.push("Write the announcement.");
	else if (body.length > BODY_MAX)
		problems.push(`Keep the announcement under ${BODY_MAX} characters.`);
	if (draft.link.trim() && !isSafeHref(draft.link))
		problems.push("The link must be a web address or a page in the app.");
	if (draft.expiresAt) {
		const when = new Date(draft.expiresAt);
		if (Number.isNaN(when.getTime()) || when.getTime() <= now.getTime())
			problems.push("The expiry must be in the future.");
	}
	if (draft.audience === "workspace" && draft.workspaceId === null)
		problems.push("Choose the workspace to send to.");
	return problems;
}

export function draftToBody(draft: AnnouncementDraft): AnnouncementBody {
	return {
		title: draft.title.trim(),
		body: draft.body.trim(),
		link: draft.link.trim() || null,
		expires_at: draft.expiresAt ? new Date(draft.expiresAt).toISOString() : null,
		audience: draft.audience,
		workspace_id: draft.audience === "workspace" ? draft.workspaceId : null,
	};
}
