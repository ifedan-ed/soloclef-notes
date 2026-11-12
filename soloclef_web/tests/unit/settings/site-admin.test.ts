import assert from "node:assert/strict";
import test from "node:test";
import { audienceOf, showsSettingsTab } from "@/features/settings-visibility/settings-visibility";
import { siteAdminStanding } from "@/hooks/use-site-admin";

// Run with: pnpm exec tsx --test tests/unit/settings/site-admin.test.ts

const profile = (is_superuser: boolean) => ({ is_superuser });

test("the profile's superuser flag decides who administers the site", () => {
	assert.deepEqual(siteAdminStanding({ data: profile(true), isError: false, isLoading: false }), {
		isAdmin: true,
		isLoading: false,
	});
	assert.deepEqual(siteAdminStanding({ data: profile(false), isError: false, isLoading: false }), {
		isAdmin: false,
		isLoading: false,
	});
});

test("while the profile loads nobody is decided, so admin pages wait", () => {
	const loading = siteAdminStanding({ isError: false, isLoading: true });
	assert.deepEqual(loading, { isAdmin: undefined, isLoading: true });
	assert.equal(audienceOf(loading.isAdmin), "unknown");
});

test("a profile that failed to load is treated as a member, and admin tabs stay hidden", () => {
	const failed = siteAdminStanding({ isError: true, isLoading: false });
	assert.deepEqual(failed, { isAdmin: false, isLoading: false });
	const who = audienceOf(failed.isAdmin);
	assert.equal(showsSettingsTab("announcements", who), false);
	assert.equal(showsSettingsTab("profile", who), true);
});
