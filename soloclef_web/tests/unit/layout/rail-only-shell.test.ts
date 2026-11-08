import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

// The dashboard has one sidebar, the rail. Home is on the rail; the account
// menu (the user avatar) holds the workspace switcher, settings, "Connect your
// agent" and notifications. These read the sources, like the other layout
// tests, since the menus only render when opened.

const root = process.cwd();
const read = (path: string) => readFileSync(join(root, path), "utf8");

test("the second sidebar, its resize and collapse, and its notebook list are gone", () => {
	for (const gone of [
		"components/layout/ui/sidebar/Sidebar.tsx",
		"components/layout/ui/sidebar/SidebarHeader.tsx",
		"components/layout/ui/sidebar/SidebarCollapseButton.tsx",
		"components/layout/hooks/useSidebarState.ts",
		"components/layout/hooks/useSidebarResize.ts",
		"components/layout/sidebar-preferences.ts",
		"features/notebooks/ui/nav/recent-notebooks-nav.tsx",
	]) {
		assert.equal(existsSync(join(root, gone)), false, gone);
	}
	const shell = read("components/layout/ui/shell/LayoutShell.tsx");
	assert.doesNotMatch(shell, /<Sidebar\b|renderNavSlot|Resize sidebar|initialSidebar/);
	assert.match(shell, /<IconRail[\s\S]*navItems=\{navItems\}/);
	const layout = read("app/dashboard/[workspace_id]/layout.tsx");
	assert.doesNotMatch(layout, /sidebar/i);
});

test("the rail puts Home first, then the workspaces, then the account menu", () => {
	const rail = read("components/layout/ui/icon-rail/IconRail.tsx");
	const home = rail.indexOf("navItems.map");
	const workspaces = rail.indexOf("workspaces.map");
	const account = rail.indexOf("<SidebarUserProfile");
	assert.ok(home > -1 && home < workspaces && workspaces < account);
	assert.match(rail, /aria-current=\{isActive \? "page" : undefined\}/);
	assert.match(rail, /<nav\s+aria-label=/);
	assert.match(rail, /showConnectAgent/);
	assert.match(rail, /onNotifications=/);
	// The bell stays on the rail, opened by the menu too.
	assert.match(rail, /<NotificationsDropdown[\s\S]*open=\{notificationsOpen\}/);

	const provider = read("components/layout/providers/LayoutDataProvider.tsx");
	assert.match(provider, /title: tSidebar\("home"\)/);
});

test("the account menu holds the switcher, settings, Connect your agent and Notifications", () => {
	const menu = read("components/layout/ui/sidebar/SidebarUserProfile.tsx");
	for (const key of [
		"switch_workspace",
		"manage_members",
		"workspace_settings",
		"user_settings",
		"connect_agent",
		"notifications",
		"theme",
		"logout",
	]) {
		assert.match(menu, new RegExp(`t\\("${key}"`), key);
	}
	// Notifications is the only inbox; the static "What's New" is gone.
	assert.match(menu, /onNotifications \? \(/);
	assert.doesNotMatch(menu, /onAnnouncements|What's New/);
	// Actions run after the menu closes, so the dialog they open keeps focus.
	assert.match(menu, /onCloseAutoFocus=\{runPendingAction\}/);
	// The avatar is named for screen readers and shows a focus ring.
	assert.match(menu, /aria-label=\{t\("account_menu"/);
	assert.match(menu, /focus-visible:ring-2/);

	const dialog = read("components/mcp/connect-agent-dialog.tsx");
	assert.doesNotMatch(dialog, /DialogTrigger/);
	assert.match(dialog, /open=\{open\} onOpenChange=\{onOpenChange\}/);
});

test("the phone sheet keeps Home, the workspaces and the account menu", () => {
	const sheet = read("components/layout/ui/sidebar/MobileSidebar.tsx");
	assert.doesNotMatch(sheet, /<Sidebar\b/);
	assert.match(sheet, /navItems\.map/);
	assert.match(sheet, /workspaces\.map/);
	assert.match(sheet, /<SidebarUserProfile[\s\S]*showConnectAgent/);
	assert.match(sheet, /<NotificationsDropdown/);
});

test("the notebooks home names the open workspace", () => {
	const home = read("features/notebooks/ui/home/notebooks-home.tsx");
	assert.match(home, /cacheKeys\.workspaces\.detail/);
	assert.match(home, /\{workspace\.name\}/);
});
