import assert from "node:assert/strict";
import test from "node:test";

import { classifyResetError, newPasswordProblem } from "@/features/password-reset/lib/reset-errors";
import { AppError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/password-reset/reset-errors.test.ts

test("a used or expired link says so", () => {
	assert.equal(
		classifyResetError(new AppError("RESET_PASSWORD_BAD_TOKEN", 400)),
		"reset_link_invalid"
	);
});

test("a password the server refuses asks for a longer one", () => {
	const error = new AppError("{...}", 400, "Bad Request", "RESET_PASSWORD_INVALID_PASSWORD");
	assert.equal(classifyResetError(error), "password_too_short");
});

test("security check refusals are told apart from an outage", () => {
	assert.equal(classifyResetError(new AppError("TURNSTILE_FAILED", 400)), "security_check_failed");
	assert.equal(
		classifyResetError(new AppError("TURNSTILE_REQUIRED", 400)),
		"security_check_failed"
	);
	assert.equal(
		classifyResetError(new AppError("TURNSTILE_UNAVAILABLE", 503)),
		"security_check_unavailable"
	);
});

test("rate limits and anything else get their own message", () => {
	assert.equal(classifyResetError(new AppError("RATE_LIMIT_EXCEEDED", 429)), "too_many_attempts");
	assert.equal(classifyResetError(new Error("boom")), "reset_failed");
	assert.equal(classifyResetError("weird"), "reset_failed");
});

test("the new password is checked before asking the server", () => {
	assert.equal(newPasswordProblem("short", "short"), "password_too_short");
	assert.equal(newPasswordProblem("long enough 1", "long enough 2"), "password_mismatch");
	assert.equal(newPasswordProblem("long enough 1", "long enough 1"), null);
});
