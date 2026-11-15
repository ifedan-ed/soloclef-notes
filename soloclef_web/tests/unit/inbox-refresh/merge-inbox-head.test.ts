import assert from "node:assert/strict";
import { test } from "node:test";
import { mergeInboxHead, stillHidden } from "@/features/inbox-refresh/lib/merge-inbox-head";

/** An inbox item made `hoursAgo` hours before a fixed moment; `read` marks a fresher copy. */
const item = (id: number, hoursAgo: number, read = false) => ({
	id,
	read,
	created_at: new Date(Date.UTC(2026, 10, 13, 12) - hoursAgo * 3_600_000).toISOString(),
});

const ids = (items: { id: number }[]) => items.map((i) => i.id);

test("the first head is the list", () => {
	const head = { items: [item(3, 1), item(2, 2)], has_more: true };
	assert.deepEqual(ids(mergeInboxHead([], head, new Set())), [3, 2]);
});

test("a new notification lands on top; a fresher copy replaces the shown one", () => {
	const shown = [item(2, 2), item(1, 3)];
	const head = { items: [item(3, 1), item(2, 2, true), item(1, 3)], has_more: false };
	const merged = mergeInboxHead(shown, head, new Set());
	assert.deepEqual(ids(merged), [3, 2, 1]);
	assert.equal(merged[1].read, true);
});

test("an item in the head's range that the head lacks was deleted elsewhere", () => {
	const shown = [item(3, 1), item(2, 2), item(1, 3)];
	const head = { items: [item(3, 1), item(1, 3)], has_more: false };
	assert.deepEqual(ids(mergeInboxHead(shown, head, new Set())), [3, 1]);
});

test("older pages loaded by scrolling stay when the head has more after it", () => {
	const shown = [item(5, 1), item(4, 2), item(2, 10), item(1, 11)];
	const head = { items: [item(6, 0), item(5, 1), item(4, 2)], has_more: true };
	assert.deepEqual(ids(mergeInboxHead(shown, head, new Set())), [6, 5, 4, 2, 1]);
});

test("a head with nothing after it is the whole inbox: older shown items are gone", () => {
	const shown = [item(5, 1), item(2, 10)];
	const head = { items: [item(5, 1)], has_more: false };
	assert.deepEqual(ids(mergeInboxHead(shown, head, new Set())), [5]);
});

test("an item deleted here stays out while the server still lists it", () => {
	const head = { items: [item(3, 1), item(2, 2)], has_more: false };
	assert.deepEqual(ids(mergeInboxHead([item(3, 1)], head, new Set([2]))), [3]);
});

test("a hidden id is forgotten once a head no longer lists it", () => {
	const head = { items: [item(3, 1)], has_more: false };
	assert.deepEqual([...stillHidden(new Set([2, 3]), head)], [3]);
});
