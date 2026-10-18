import assert from "node:assert/strict";
import test from "node:test";

import { getAuthErrorDetails } from "@/lib/auth-errors";
import { buildSsoStartUrl } from "@/lib/sso-login";

// Run with: pnpm exec tsx --test tests/unit/sso-login/sso-login.test.ts

test("the button starts the backend's single sign-on redirect", () => {
	assert.equal(buildSsoStartUrl(), "/auth/oidc/authorize-redirect");
	assert.equal(buildSsoStartUrl(null), "/auth/oidc/authorize-redirect");
});

test("the page the person was sent away from rides along", () => {
	assert.equal(
		buildSsoStartUrl("/notebooks/9?tab=sources"),
		"/auth/oidc/authorize-redirect?next=%2Fnotebooks%2F9%3Ftab%3Dsources"
	);
});

test("an address on another site is never passed on", () => {
	assert.equal(buildSsoStartUrl("//evil.example/"), "/auth/oidc/authorize-redirect");
	assert.equal(buildSsoStartUrl("https://evil.example/"), "/auth/oidc/authorize-redirect");
});

test("every refusal the callback can send is explained in words", () => {
	// The codes in surfsense_backend/app/auth_oidc/errors.py.
	for (const code of [
		"OIDC_NO_ACCOUNT",
		"OIDC_EMAIL_NOT_VERIFIED",
		"OIDC_ACCOUNT_NOT_VERIFIED",
		"OIDC_INVALID_STATE",
		"OIDC_INVALID_ID_TOKEN",
		"OIDC_EXCHANGE_FAILED",
		"OIDC_PROVIDER_ERROR",
		"OIDC_UNAVAILABLE",
		"USER_INACTIVE",
		"RATE_LIMIT_EXCEEDED",
		"access_denied",
	]) {
		const details = getAuthErrorDetails(code);
		assert.notEqual(details.title, "Login failed", `${code} falls back to the generic message`);
	}
	assert.equal(getAuthErrorDetails("OIDC_NO_ACCOUNT").title, "No account for this address");
});
