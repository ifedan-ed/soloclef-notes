import assert from "node:assert/strict";
import { test } from "node:test";
import {
	audienceOf,
	leavesTab,
	showsUserSettingsTab,
	showsWorkspaceSettingsTab,
} from "../../../features/settings-visibility/settings-visibility";

test("community prompts, agent permissions and announcements are for administrators", () => {
	for (const tab of ["community-prompts", "agent-permissions", "announcements"]) {
		assert.equal(showsUserSettingsTab(tab, "admin"), true);
		assert.equal(showsUserSettingsTab(tab, "member"), false);
		assert.equal(showsUserSettingsTab(tab, "unknown"), false);
	}
	assert.equal(showsUserSettingsTab("prompts", "member"), true);
	assert.equal(showsUserSettingsTab("profile", "unknown"), true);
});

test("everyone can open API keys, which Connect your agent tells them to use", () => {
	for (const who of ["admin", "member", "unknown"] as const) {
		assert.equal(showsUserSettingsTab("api-key", who), true);
	}
});

test("no workspace settings tab is Owner-only now that the connected repo page is gone", () => {
	for (const tab of [
		"general",
		"connectors",
		"automations",
		"public-links",
		"team-roles",
		"prompts",
	]) {
		assert.equal(showsWorkspaceSettingsTab(tab, "member"), true);
	}
});

test("an admin page leaves only once we know who is looking", () => {
	assert.equal(audienceOf(undefined), "unknown");
	assert.equal(audienceOf(false), "member");
	assert.equal(leavesTab("agent-permissions", "unknown", showsUserSettingsTab), false);
	assert.equal(leavesTab("agent-permissions", "member", showsUserSettingsTab), true);
	assert.equal(leavesTab("agent-permissions", "admin", showsUserSettingsTab), false);
	assert.equal(leavesTab("api-key", "member", showsUserSettingsTab), false);
	assert.equal(leavesTab("general", "member", showsWorkspaceSettingsTab), false);
	assert.equal(leavesTab("profile", "member", showsUserSettingsTab), false);
});
