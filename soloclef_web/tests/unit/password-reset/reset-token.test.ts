import assert from "node:assert/strict";
import test from "node:test";

import { readResetToken } from "@/features/password-reset/lib/reset-token";

// Run with: pnpm exec tsx --test tests/unit/password-reset/reset-token.test.ts

test("the email's link carries the token in the fragment", () => {
	assert.equal(readResetToken("#token=abc.def-ghi", ""), "abc.def-ghi");
});

test("a query-string link still works", () => {
	assert.equal(readResetToken("", "?token=xyz"), "xyz");
});

test("the fragment wins over the query", () => {
	assert.equal(readResetToken("#token=frag", "?token=query"), "frag");
});

test("no token, or an empty one, is no token", () => {
	assert.equal(readResetToken("", ""), null);
	assert.equal(readResetToken("#token=", ""), null);
	assert.equal(readResetToken("#other=1", "?x=2"), null);
});
