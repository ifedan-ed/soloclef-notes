import assert from "node:assert/strict";
import { test } from "node:test";
import {
	clearConfirmTitle,
	clearScopeFor,
	isCleared,
	isRunning,
} from "../../../features/inbox-clearing/lib/clear-scope";

test("the Unread tab clears only unread items; every other tab clears all of its items", () => {
	const read = { read: true };
	const unread = { read: false };

	for (const tab of ["all", "mentions", "announcements"] as const) {
		assert.equal(isCleared(read, clearScopeFor(tab)), true, tab);
		assert.equal(isCleared(unread, clearScopeFor(tab)), true, tab);
	}
	assert.equal(isCleared(read, clearScopeFor("unread")), false);
	assert.equal(isCleared(unread, clearScopeFor("unread")), true);
});

test("a running job's item is never cleared, read or not, on any tab", () => {
	const running = { metadata: { status: "in_progress" } };
	for (const tab of ["all", "mentions", "announcements", "unread"] as const) {
		assert.equal(isCleared({ ...running, read: false }, clearScopeFor(tab)), false, tab);
		assert.equal(isCleared({ ...running, read: true }, clearScopeFor(tab)), false, tab);
	}
	for (const status of ["completed", "failed", undefined]) {
		assert.equal(isRunning({ metadata: { status } }), false, String(status));
		assert.equal(isCleared({ read: false, metadata: { status } }, clearScopeFor("all")), true);
	}
	assert.equal(isRunning({ metadata: null }), false);
	assert.equal(isRunning({}), false);
});

test("the question names what the tab clears", () => {
	assert.equal(clearConfirmTitle("all"), "Clear all notifications?");
	assert.equal(clearConfirmTitle("mentions"), "Clear all mentions?");
	assert.equal(clearConfirmTitle("announcements"), "Clear all announcements?");
	assert.equal(clearConfirmTitle("unread"), "Clear all unread notifications?");
});
