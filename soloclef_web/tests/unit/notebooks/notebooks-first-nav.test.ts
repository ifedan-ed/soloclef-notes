import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import {
	hasRetiredChatParam,
	withoutRetiredChatParam,
} from "@/features/notebooks/lib/retired-chat-param";
import { NOTEBOOKS_HOME, notebookPath } from "@/lib/dashboard-paths";

test("an old notebook link's chat parameter is dropped, the rest kept", () => {
	assert.equal(hasRetiredChatParam("?chat=42"), true);
	assert.equal(hasRetiredChatParam("?source=3&chat="), true);
	assert.equal(hasRetiredChatParam("?source=3"), false);
	assert.equal(hasRetiredChatParam(""), false);
	assert.equal(
		withoutRetiredChatParam("https://notes.example/dashboard/1/notebooks/5?chat=42&commentId=3"),
		"https://notes.example/dashboard/1/notebooks/5?commentId=3"
	);
	assert.equal(
		withoutRetiredChatParam("https://notes.example/dashboard/1/notebooks/5?chat=42"),
		"https://notes.example/dashboard/1/notebooks/5"
	);
});

test("a link to a chat opens its notebook, or the home, never naming the chat", () => {
	assert.equal(notebookPath(5), "/dashboard/notebooks/5");
	for (const none of [null, undefined, 0, -2, 1.5]) {
		assert.equal(notebookPath(none), NOTEBOOKS_HOME, String(none));
	}
});

test("the retired chat routes are gone", () => {
	const root = process.cwd();
	for (const page of ["app/dashboard/new-chat", "app/dashboard/chats"]) {
		assert.equal(existsSync(join(root, page)), false, page);
	}
	for (const gone of [
		"components/layout/ui/sidebar/AllChatsSidebar.tsx",
		"components/layout/ui/sidebar/ChatListItem.tsx",
		"components/layout/ui/tabs/TabBar.tsx",
		"atoms/tabs/tabs.atom.ts",
		"features/notebooks/ui/legacy-chat/legacy-chat-redirect.tsx",
		"features/notebooks/lib/legacy-chat-link.ts",
	]) {
		assert.equal(existsSync(join(root, gone)), false, gone);
	}
});

test("connectors and automations live in settings", () => {
	const root = process.cwd();
	const shell = readFileSync(join(root, "app/dashboard/settings/layout-shell.tsx"), "utf8");
	assert.match(shell, /item\("connectors"/);
	assert.match(shell, /item\("automations"/);
	const automations = readFileSync(join(root, "app/dashboard/automations/page.tsx"), "utf8");
	assert.match(automations, /settingsPath\("automations"\)/);
});
