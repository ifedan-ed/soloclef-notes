import assert from "node:assert/strict";
import test from "node:test";
import { inboxTarget } from "@/features/inbox-targets/lib/inbox-target";
import { waitingDraftsSchema } from "@/features/mail-drafts-waiting/api/waiting-drafts-api";

// Run with: pnpm exec tsx --test tests/unit/mail-drafts-waiting/waiting-drafts.test.ts

test("the list reads the server's answer, an empty one included", () => {
	const listed = waitingDraftsSchema.parse({
		drafts: [
			{
				id: "0f1e2d3c-4b5a-4968-8776-655443322110",
				status: "pending",
				subject: "Invoice 4411",
				account_label: "Work",
				created_at: "2026-11-13T09:00:00Z",
				expires_at: "2026-11-14T09:00:00Z",
			},
		],
		more: false,
	});
	assert.equal(listed.drafts[0].account_label, "Work");
	assert.deepEqual(waitingDraftsSchema.parse({ drafts: [], more: false }).drafts, []);
});

test("a status the card does not draw is refused, not drawn as something else", () => {
	assert.throws(() =>
		waitingDraftsSchema.parse({
			drafts: [
				{
					id: "x",
					status: "queued",
					subject: "",
					account_label: "",
					created_at: "2026-11-13T09:00:00Z",
					expires_at: "2026-11-14T09:00:00Z",
				},
			],
			more: false,
		})
	);
});

test("a waiting draft's inbox row opens the Drafts waiting page", () => {
	// The link the server writes (soloclef_assistant/maildraftnotice.WaitingPage).
	const row = { type: "mail_draft", metadata: { draft_id: "d-1", link: "/dashboard/mail/drafts" } };
	// inboxTarget answers the app path itself (an App Store row is inboxStoreLink's).
	assert.equal(inboxTarget(row), "/dashboard/mail/drafts");
});
