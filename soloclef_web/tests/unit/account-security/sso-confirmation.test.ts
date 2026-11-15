import assert from "node:assert/strict";
import test from "node:test";
import {
	cameBackConfirmed,
	confirmedReturnPath,
	ssoConfirmationUrl,
} from "@/features/account-security/lib/sso-confirmation";

// Run with: pnpm exec tsx --test tests/unit/account-security/sso-confirmation.test.ts

test("the provider brings the browser back to Security, marked as confirmed", () => {
	assert.equal(confirmedReturnPath(), "/dashboard/settings/security?confirmed=sso");
	const url = new URL(ssoConfirmationUrl(), "https://notes.example");
	assert.equal(url.pathname, "/auth/oidc/authorize-redirect");
	// The return path travels whole, its own query included.
	assert.equal(url.searchParams.get("next"), "/dashboard/settings/security?confirmed=sso");
});

test("only the exact marker counts as coming back", () => {
	assert.equal(cameBackConfirmed(new URLSearchParams("confirmed=sso")), true);
	assert.equal(cameBackConfirmed(new URLSearchParams("confirmed=yes")), false);
	assert.equal(cameBackConfirmed(new URLSearchParams("")), false);
	assert.equal(cameBackConfirmed(null), false);
});
