import assert from "node:assert/strict";
import test from "node:test";

import {
	isDeletedAutomationKey,
	markAutomationDeleted,
	unmarkAutomationDeleted,
} from "@/lib/automations/deleted-automations";

test("a deleted automation's detail and runs queries are expected to 404", () => {
	markAutomationDeleted(41);
	assert.equal(isDeletedAutomationKey(["automations", "detail", 41]), true);
	assert.equal(isDeletedAutomationKey(["automations", "runs", 41, 50, 0]), true);
	assert.equal(isDeletedAutomationKey(["automations", "runs", 41, 7]), true);
});

test("other automations and other queries still report errors", () => {
	markAutomationDeleted(42);
	assert.equal(isDeletedAutomationKey(["automations", "detail", 43]), false);
	assert.equal(isDeletedAutomationKey(["automations", "list", 42, 50, 0]), false);
	assert.equal(isDeletedAutomationKey(["notebooks", "detail", 42]), false);
	assert.equal(isDeletedAutomationKey(undefined), false);
});

test("a failed delete is unmarked", () => {
	markAutomationDeleted(44);
	unmarkAutomationDeleted(44);
	assert.equal(isDeletedAutomationKey(["automations", "detail", 44]), false);
});
