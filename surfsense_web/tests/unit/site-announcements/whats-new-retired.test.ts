import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { showsUserSettingsTab } from "../../../features/settings-visibility/settings-visibility";

const root = process.cwd();
const read = (path: string) => readFileSync(join(root, path), "utf8");

test("the static What's New dialog, spotlight, toasts and data are gone", () => {
	for (const gone of [
		"components/announcements",
		"lib/announcements",
		"hooks/use-announcements.ts",
		"contracts/types/announcement.types.ts",
		"atoms/layout/dialogs.atom.ts",
	]) {
		assert.equal(existsSync(join(root, gone)), false, gone);
	}
	assert.doesNotMatch(read("app/layout.tsx"), /Announcement/);
	assert.doesNotMatch(
		read("components/layout/providers/LayoutDataProvider.tsx"),
		/useAnnouncements/
	);
});

test("the Notifications panel has an Announcements tab fed by its own inbox", () => {
	const panel = read("components/layout/ui/sidebar/NotificationsDropdown.tsx");
	assert.match(panel, /value: "announcements", label: "Announcements"/);
	const provider = read("components/layout/providers/LayoutDataProvider.tsx");
	assert.match(provider, /useInbox\(\s*userId,\s*numericSpaceId,\s*"announcements"/);
	assert.match(provider, /announcements: \{\s*items: announcementsInbox\.inboxItems/);
});

test("only administrators see the Announcements settings page", () => {
	assert.equal(showsUserSettingsTab("announcements", "admin"), true);
	assert.equal(showsUserSettingsTab("announcements", "member"), false);
	assert.equal(showsUserSettingsTab("announcements", "unknown"), false);
	assert.ok(
		existsSync(join(root, "app/dashboard/[workspace_id]/user-settings/announcements/page.tsx"))
	);
});
