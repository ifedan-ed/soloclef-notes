import assert from "node:assert/strict";
import test from "node:test";

import {
	humanCheckHeaders,
	TURNSTILE_TOKEN_HEADER,
} from "@/features/human-check/lib/turnstile-script";

// Run with: pnpm exec tsx --test tests/unit/human-check/turnstile-headers.test.ts

test("a solved check travels in the header the backend reads", () => {
	assert.equal(TURNSTILE_TOKEN_HEADER, "X-Turnstile-Token");
	assert.deepEqual(humanCheckHeaders("tok"), { "X-Turnstile-Token": "tok" });
});

test("no token means no header, so a deployment without keys sends nothing", () => {
	assert.deepEqual(humanCheckHeaders(null), {});
	assert.deepEqual(humanCheckHeaders(undefined), {});
	assert.deepEqual(humanCheckHeaders(""), {});
});
