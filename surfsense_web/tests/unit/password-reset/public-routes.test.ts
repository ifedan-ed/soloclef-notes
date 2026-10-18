import assert from "node:assert/strict";
import test from "node:test";

import { isPublicRoute } from "@/lib/public-routes";

// Run with: pnpm exec tsx --test tests/unit/password-reset/public-routes.test.ts

test("the password pages open without a session", () => {
	assert.equal(isPublicRoute("/forgot-password"), true);
	assert.equal(isPublicRoute("/reset-password"), true);
	assert.equal(isPublicRoute("/dashboard/1/notebooks"), false);
});
