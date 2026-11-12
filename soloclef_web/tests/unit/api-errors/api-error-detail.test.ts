import assert from "node:assert/strict";
import test from "node:test";
import { apiErrorDetail } from "@/lib/api-error-detail";
import { AppError, AuthorizationError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/api-errors/api-error-detail.test.ts

test("the server's own words are shown when they say something a person can act on", () => {
	const taken = new AppError("A folder with that name already exists here.", 409);
	assert.equal(apiErrorDetail(taken, "fallback"), taken.message);
	assert.equal(
		apiErrorDetail(new AuthorizationError("Turn on API access in Settings.", 403), "f"),
		"Turn on API access in Settings."
	);
});

test("a generic or foreign error falls back to the caller's words", () => {
	assert.equal(apiErrorDetail(new AppError("Something went wrong", 500), "fallback"), "fallback");
	assert.equal(apiErrorDetail(new AppError("Failed to parse response", 500), "fallback"), "fallback");
	assert.equal(apiErrorDetail(new Error("boom"), "fallback"), "fallback");
});
