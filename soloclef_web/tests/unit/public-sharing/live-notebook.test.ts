import assert from "node:assert/strict";
import test from "node:test";

import { LiveLinkError } from "@/features/public-sharing/api/live-notebook-api";
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

test("a link row is the owner's own link: no creator and no revoke flag", () => {
	const row = liveLinkSchema.parse({
		id: 1,
		notebook_id: 2,
		notebook_title: "Ward 4",
		notebook_icon: null,
		share_token: "t",
		public_url: "https://x/public/notebook/t",
		created_at: "2026-09-27T10:00:00Z",
		expires_at: null,
		is_expired: false,
	});
	assert.equal(row.is_expired, false);
	assert.equal("can_revoke" in row, false);
});
