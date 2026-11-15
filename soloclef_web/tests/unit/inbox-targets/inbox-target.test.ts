import assert from "node:assert/strict";
import { test } from "node:test";
import { getNotificationsRequest, inboxItem } from "@/contracts/types/inbox.types";
import { inboxStoreLink, inboxTarget, retryHref } from "@/features/inbox-targets/lib/inbox-target";
import { automationRunPath, readRunParam } from "@/lib/automations/run-link";

/** Where opening each kind of inbox item goes, and the failed Studio output's "Try again". */

const studio = (metadata: Record<string, unknown>) => ({
	type: "studio_output",
	metadata: {
		status: "completed",
		notebook_id: 11,
		notebook_title: "Biology",
		output_id: 42,
		output_kind: "report",
		output_title: "Cell report",
		retry: false,
		...metadata,
	},
});

test("a Studio output opens its notebook with Studio showing", () => {
	assert.equal(inboxTarget(studio({})), "/dashboard/notebooks/11?pane=studio");
	// A notebook deleted since (no id): nowhere to go, opening only marks it read.
	assert.equal(inboxTarget(studio({ notebook_id: null })), null);
});

test("a failed output the server marked retryable offers Try again; nothing else does", () => {
	assert.equal(
		retryHref(studio({ status: "failed", retry: true })),
		"/dashboard/notebooks/11?pane=studio&retry_output=42"
	);
	assert.equal(retryHref(studio({ status: "failed", retry: false })), null);
	assert.equal(retryHref(studio({ status: "completed", retry: true })), null);
	assert.equal(retryHref(studio({ status: "failed", retry: true, output_id: null })), null);
	assert.equal(retryHref(studio({ status: "failed", retry: true, notebook_id: null })), null);
	assert.equal(
		retryHref({ type: "automation_run", metadata: { status: "failed", retry: true } }),
		null
	);
});

test("an automation run opens the automation with that run open", () => {
	const item = {
		type: "automation_run",
		metadata: {
			status: "failed",
			run_status: "timed_out",
			automation_id: 7,
			automation_name: "Morning digest",
			run_id: 301,
		},
	};
	assert.equal(inboxTarget(item), "/dashboard/automations/7?run=301");
	assert.equal(automationRunPath(7, 301), "/dashboard/automations/7?run=301");
	assert.equal(readRunParam("?run=301"), 301);
	assert.equal(readRunParam("?run=abc"), null);
	assert.equal(readRunParam(""), null);
	assert.equal(inboxTarget({ ...item, metadata: { ...item.metadata, run_id: undefined } }), null);
});

test("a mail draft opens the page its link names, only inside this site", () => {
	const draft = (link: unknown) => ({
		type: "mail_draft",
		metadata: { status: "completed", draft_id: "d-1", link },
	});
	assert.equal(inboxTarget(draft("/dashboard/mail/drafts/d-1")), "/dashboard/mail/drafts/d-1");
	assert.equal(inboxTarget(draft(null)), null);
	assert.equal(inboxTarget(draft("//evil.example/x")), null);
	assert.equal(inboxTarget(draft("/\\evil.example")), null);
	assert.equal(inboxTarget(draft("https://evil.example")), null);
	assert.equal(inboxTarget(draft("javascript:alert(1)")), null);
});

test("uploads and connector syncs go nowhere, as before", () => {
	assert.equal(
		inboxTarget({
			type: "document_processing",
			metadata: { status: "failed", count: 3, names: ["a.pdf"], notebook_id: 11 },
		}),
		null
	);
	assert.equal(inboxTarget({ type: "connector_indexing", metadata: {} }), null);
});

test("the inbox's contract takes the new types, as rows and as the list's type filter", () => {
	for (const type of ["studio_output", "automation_run", "mail_draft", "app_update"]) {
		const row = {
			id: 1,
			user_id: "u",
			type,
			title: "t",
			message: "m",
			read: false,
			metadata: { push: { kind: "output", body: "", path: "/x" }, collapse_key: "k" },
			created_at: "2026-11-12T10:00:00Z",
			updated_at: null,
		};
		assert.equal(inboxItem.safeParse(row).success, true, type);
		assert.equal(getNotificationsRequest.safeParse({ queryParams: { type } }).success, true, type);
	}
});

test("an App Store update opens its listing in a new tab, and only an App Store page", () => {
	const update = (storeUrl: unknown) => ({
		type: "app_update",
		metadata: { status: "completed", version: "1.2.0", store_url: storeUrl },
	});
	assert.equal(
		inboxStoreLink(update("https://apps.apple.com/app/id6821393434")),
		"https://apps.apple.com/app/id6821393434"
	);
	// It is not a page of the app.
	assert.equal(inboxTarget(update("https://apps.apple.com/app/id6821393434")), null);
	for (const bad of [
		"https://evil.example/app",
		"http://apps.apple.com/app/id1",
		"javascript:alert(1)",
		"//apps.apple.com",
		null,
		7,
	]) {
		assert.equal(inboxStoreLink(update(bad)), null, String(bad));
	}
	// No other item opens an outside page, whatever it carries.
	assert.equal(
		inboxStoreLink({
			type: "mail_draft",
			metadata: { store_url: "https://apps.apple.com/app/id1" },
		}),
		null
	);
});
