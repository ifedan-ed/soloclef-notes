import assert from "node:assert/strict";
import { test } from "node:test";
import {
	audienceOf,
	leavesTab,
	showsSettingsTab,
} from "../../../features/settings-visibility/settings-visibility";

test("community prompts, agent permissions and announcements are for administrators", () => {
	for (const tab of ["community-prompts", "agent-permissions", "announcements"]) {
		assert.equal(showsSettingsTab(tab, "admin"), true);
		assert.equal(showsSettingsTab(tab, "member"), false);
		assert.equal(showsSettingsTab(tab, "unknown"), false);
	}
	assert.equal(showsSettingsTab("prompts", "member"), true);
	assert.equal(showsSettingsTab("profile", "unknown"), true);
});

test("everyone can open API keys, which Connect your agent tells them to use", () => {
	for (const who of ["admin", "member", "unknown"] as const) {
		assert.equal(showsSettingsTab("api-key", who), true);
	}
});

test("the account's own sections are shown to every account", () => {
	for (const tab of [
		"models",
		"instructions",
		"memory",
		"connectors",
		"automations",
		"public-links",
	]) {
		assert.equal(showsSettingsTab(tab, "member"), true);
		assert.equal(showsSettingsTab(tab, "unknown"), true);
	}
});

test("an admin page leaves only once we know who is looking", () => {
	assert.equal(audienceOf(undefined), "unknown");
	assert.equal(audienceOf(false), "member");
	assert.equal(leavesTab("agent-permissions", "unknown", showsSettingsTab), false);
	assert.equal(leavesTab("agent-permissions", "member", showsSettingsTab), true);
	assert.equal(leavesTab("agent-permissions", "admin", showsSettingsTab), false);
	assert.equal(leavesTab("api-key", "member", showsSettingsTab), false);
	assert.equal(leavesTab("profile", "member", showsSettingsTab), false);
});
