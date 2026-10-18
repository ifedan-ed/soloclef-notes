import assert from "node:assert/strict";
import test from "node:test";

import { LiveLinkError } from "@/features/public-sharing/api/live-notebook-api";
import {
	canCreatePublicLinks,
	canViewPublicLinks,
} from "@/features/public-sharing/lib/public-link-access";
import { publicLinkFailure } from "@/features/public-sharing/lib/public-link-failure";
import {
	liveNotebookApiPath,
	liveNotebookUrl,
} from "@/features/public-sharing/lib/public-link-url";
import { liveLinkSchema } from "@/features/public-sharing/model/live-link";
import {
	liveNotebookSchema,
	liveSourceViewSchema,
} from "@/features/public-sharing/model/live-notebook";

// Run with: pnpm exec tsx --test tests/unit/public-sharing/live-notebook.test.ts

test("a live link opens at /public/notebook/<token> on the app's own origin", () => {
	assert.equal(
		liveNotebookUrl("https://notes.example/", "abc_DEF-1"),
		"https://notes.example/public/notebook/abc_DEF-1"
	);
});

test("the token cannot steer the public API to another path", () => {
	assert.equal(liveNotebookApiPath("abc"), "/api/v1/public-notebooks/abc");
	assert.equal(
		liveNotebookApiPath("../../workspaces/1", "/sources/2"),
		"/api/v1/public-notebooks/..%2F..%2Fworkspaces%2F1/sources/2"
	);
});

test("members who may see links see the badge; only owners by default make one", () => {
	const viewer = { is_owner: false, permissions: ["public_sharing:view", "chats:read"] };
	assert.equal(canViewPublicLinks(viewer), true);
	assert.equal(canCreatePublicLinks(viewer), false);
	assert.equal(canViewPublicLinks({ is_owner: true }), true);
	assert.equal(canViewPublicLinks({ is_owner: false, permissions: [] }), false);
	assert.equal(canViewPublicLinks(null), false);
});

test("an expired link says so; a revoked or deleted one is gone", () => {
	assert.equal(publicLinkFailure(new LiveLinkError(410)), "expired");
	assert.equal(publicLinkFailure(new LiveLinkError(404)), "gone");
	assert.equal(publicLinkFailure(new LiveLinkError(500)), "error");
});

test("the page payload parses as the server sends it", () => {
	const page = liveNotebookSchema.parse({
		notebook: { title: "Ward 4 asthma", icon: null },
		expires_at: null,
		messages: [
			{
				role: "assistant",
				content: [{ type: "text", text: "Use a spacer [citation:1]." }],
				created_at: "2026-09-27T10:00:00+00:00",
			},
		],
		citations: { "1": { source_id: 7, title: "Guideline", page: 3 } },
		outputs: [
			{
				id: 1,
				kind: "report",
				title: "Discharge teaching",
				format: "html",
				artifact_id: 9,
				duration_seconds: null,
				created_at: "2026-09-27T10:00:00+00:00",
			},
		],
		sources: [{ id: 7, title: "Guideline", document_type: "FILE", has_file: true }],
	});
	assert.equal(page.citations["1"].page, 3);
	assert.throws(() =>
		liveSourceViewSchema.parse({ id: 1, title: "x", document_type: "FILE", presentation: "raw" })
	);
});

test("a link row carries whether the reader may revoke it", () => {
	const row = liveLinkSchema.parse({
		id: 1,
		notebook_id: 2,
		notebook_title: "Ward 4",
		notebook_icon: null,
		share_token: "t",
		public_url: "https://x/public/notebook/t",
		created_at: "2026-09-27T10:00:00+00:00",
		expires_at: null,
		is_expired: false,
		created_by_user_id: null,
		created_by_name: null,
		can_revoke: false,
	});
	assert.equal(row.can_revoke, false);
});
