import assert from "node:assert/strict";
import { test } from "node:test";
import {
	withoutDeletedElsewhere,
	withoutHidden,
} from "../../../features/inbox-clearing/lib/zero-deletions";

const items = [{ id: 1 }, { id: 2 }, { id: 3 }];

test("once Zero's result is complete, a row it no longer has was deleted elsewhere", () => {
	assert.deepEqual(withoutDeletedElsewhere(items, new Set([1, 3]), true), [{ id: 1 }, { id: 3 }]);
});

test("while Zero is still arriving, nothing is taken for deleted", () => {
	assert.equal(withoutDeletedElsewhere(items, new Set(), false), items);
});

test("nothing deleted keeps the same list, so nothing redraws", () => {
	assert.equal(withoutDeletedElsewhere(items, new Set([1, 2, 3, 4]), true), items);
});

test("what was deleted here stays out until Zero hears of it", () => {
	assert.deepEqual(withoutHidden(items, new Set([2])), [{ id: 1 }, { id: 3 }]);
	assert.deepEqual(withoutHidden(items, new Set()), items);
});
