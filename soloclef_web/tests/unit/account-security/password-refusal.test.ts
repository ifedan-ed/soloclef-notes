import assert from "node:assert/strict";
import test from "node:test";
import {
	BAD_CURRENT,
	CONFIRM_SSO,
	INVALID_PASSWORD,
	needsSsoConfirmation,
	passwordRefusal,
} from "@/features/account-security/lib/password-refusal";
import { AppError, AuthorizationError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/account-security/password-refusal.test.ts

test("the server's ask for a single sign-on confirmation is recognised", () => {
	// The API client turns a 403 into an AuthorizationError carrying the message.
	assert.equal(needsSsoConfirmation(new AuthorizationError(CONFIRM_SSO, 403)), true);
	// Another 403 (an API key, say) is not that ask.
	assert.equal(
		needsSsoConfirmation(new AuthorizationError("This needs a signed-in session", 403)),
		false
	);
	assert.equal(needsSsoConfirmation(new AppError(CONFIRM_SSO, 400)), false);
	assert.equal(needsSsoConfirmation(new Error(CONFIRM_SSO)), false);
});

test("each refusal has its own words", () => {
	assert.equal(passwordRefusal(new AppError(BAD_CURRENT, 400)), "bad_current");
	assert.equal(passwordRefusal(new AppError(INVALID_PASSWORD, 400)), "invalid_password");
	assert.equal(passwordRefusal(new AppError("RATE_LIMIT_EXCEEDED", 429)), "too_many_tries");
	assert.equal(passwordRefusal(new AppError("Internal Server Error", 500)), "change_failed");
	assert.equal(passwordRefusal(new Error("network")), "change_failed");
	assert.equal(passwordRefusal("odd"), "change_failed");
});
