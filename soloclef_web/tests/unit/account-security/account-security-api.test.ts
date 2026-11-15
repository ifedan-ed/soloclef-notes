import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { accountSecurityApi } from "@/features/account-security/api/account-security-api";
import { fetchSignInOptions } from "@/features/sign-in-options/api/sign-in-options-api";
import { ssoProviderName } from "@/features/sign-in-options/model/sign-in-options";
import { baseApiService } from "@/lib/apis/base-api.service";

// Run with: pnpm exec tsx --test tests/unit/account-security/account-security-api.test.ts
//
// The website calls the same routes as the phone app, with the same bodies.

type Call = { arguments: unknown[] };
const urls = (calls: Call[]) => calls.map((call) => call.arguments[0]);
const bodyOf = (call: Call) => (call.arguments[2] as { body?: unknown } | undefined)?.body;

test("the password routes", async (t) => {
	const get = mock.method(baseApiService, "get", async () => ({ has_password: true }));
	const post = mock.method(baseApiService, "post", async () => ({
		detail: "Password changed",
		signed_out_sessions: 2,
	}));
	t.after(() => mock.restoreAll());

	await accountSecurityApi.passwordState();
	assert.deepEqual(urls(get.mock.calls), ["/auth/password"]);

	await accountSecurityApi.changePassword("old password", "new password 1");
	// An account with no password sends no current password at all, not an empty one.
	await accountSecurityApi.changePassword(null, "new password 2");
	assert.deepEqual(urls(post.mock.calls), ["/auth/change-password", "/auth/change-password"]);
	assert.deepEqual(bodyOf(post.mock.calls[0]), {
		current_password: "old password",
		new_password: "new password 1",
	});
	assert.deepEqual(bodyOf(post.mock.calls[1]), { new_password: "new password 2" });
});

test("the devices routes", async (t) => {
	const get = mock.method(baseApiService, "get", async () => []);
	const del = mock.method(baseApiService, "delete", async () => null);
	const post = mock.method(baseApiService, "post", async () => ({ detail: "ok" }));
	t.after(() => mock.restoreAll());

	await accountSecurityApi.devices();
	await accountSecurityApi.signOutDevice("0b9e7d4c-1111-4222-8333-444455556666");
	await accountSecurityApi.signOutEverywhere();
	assert.deepEqual(urls(get.mock.calls), ["/auth/devices"]);
	assert.deepEqual(urls(del.mock.calls), ["/auth/devices/0b9e7d4c-1111-4222-8333-444455556666"]);
	assert.deepEqual(urls(post.mock.calls), ["/auth/jwt/logout-all"]);
});

test("sign-in options are public and name the provider when there is one", async (t) => {
	const get = mock.method(baseApiService, "get", async () => ({}));
	t.after(() => mock.restoreAll());
	await fetchSignInOptions();
	assert.deepEqual(urls(get.mock.calls), ["/api/v1/sign-in-options"]);
	assert.ok(baseApiService.noAuthEndpoints.includes("/api/v1/sign-in-options"));

	const base = { service: "soloclef", password: true, forgot_password: true, sign_up: false };
	assert.equal(
		ssoProviderName(
			{ ...base, sso: { label: "Go", name: "Example SSO", app: false } },
			"single sign-on"
		),
		"Example SSO"
	);
	assert.equal(
		ssoProviderName({ ...base, sso: { label: "Go", name: " ", app: false } }, "single sign-on"),
		"single sign-on"
	);
	assert.equal(ssoProviderName({ ...base, sso: null }, "single sign-on"), "single sign-on");
	assert.equal(ssoProviderName(undefined, "single sign-on"), "single sign-on");
});
