import assert from "node:assert/strict";
import test from "node:test";
import {
	PASSWORD_MAX,
	PASSWORD_MIN,
	passwordFormProblem,
} from "@/features/account-security/lib/password-rules";

// Run with: pnpm exec tsx --test tests/unit/account-security/password-rules.test.ts

const EMAIL = "ada@example.org";

test("an account with a password must give the current one first", () => {
	assert.equal(passwordFormProblem("", "long enough 1", "long enough 1", EMAIL), "enter_current");
	assert.equal(passwordFormProblem("old one", "long enough 1", "long enough 1", EMAIL), null);
});

test("an account with no password is not asked for one", () => {
	assert.equal(passwordFormProblem(null, "long enough 1", "long enough 1", EMAIL), null);
});

test("the length is counted in characters, as the server counts them", () => {
	// Seven emoji are fourteen UTF-16 units but seven characters: too short.
	const sevenEmoji = "🔑".repeat(PASSWORD_MIN - 1);
	assert.equal(sevenEmoji.length, (PASSWORD_MIN - 1) * 2);
	assert.equal(passwordFormProblem(null, sevenEmoji, sevenEmoji, EMAIL), "too_short");
	const eightEmoji = "🔑".repeat(PASSWORD_MIN);
	assert.equal(passwordFormProblem(null, eightEmoji, eightEmoji, EMAIL), null);
	// The longest allowed in emoji is 512 UTF-16 units and still accepted.
	const longest = "🔑".repeat(PASSWORD_MAX);
	assert.equal(passwordFormProblem(null, longest, longest, EMAIL), null);
	const tooLong = "a".repeat(PASSWORD_MAX + 1);
	assert.equal(passwordFormProblem(null, tooLong, tooLong, EMAIL), "too_long");
});

test("the email address is refused as a password, whatever its case and spaces", () => {
	assert.equal(
		passwordFormProblem(null, " ADA@example.org ", " ADA@example.org ", EMAIL),
		"same_as_email"
	);
	// Unknown email (profile not loaded yet): the server still checks.
	assert.equal(passwordFormProblem(null, EMAIL, EMAIL, undefined), null);
});

test("the two new passwords must match, checked last", () => {
	assert.equal(passwordFormProblem(null, "long enough 1", "long enough 2", EMAIL), "mismatch");
	assert.equal(passwordFormProblem(null, "short", "different", EMAIL), "too_short");
});
