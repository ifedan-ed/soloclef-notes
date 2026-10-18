import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import {
	hasRetiredChatParam,
	withoutRetiredChatParam,
} from "@/features/notebooks/lib/retired-chat-param";
import { notebookPath, workspaceHomePath } from "@/lib/workspace-home";

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
	assert.equal(notebookPath(1, 5), "/dashboard/1/notebooks/5");
	assert.equal(notebookPath("1", 5), "/dashboard/1/notebooks/5");
	for (const none of [null, undefined, 0, -2, 1.5]) {
		assert.equal(notebookPath(1, none), workspaceHomePath(1), String(none));
	}
});

test("the retired chat routes redirect to the notebooks home", () => {
	const root = process.cwd();
	for (const page of [
		"app/dashboard/[workspace_id]/new-chat/[[...chat_id]]/page.tsx",
		"app/dashboard/[workspace_id]/chats/page.tsx",
	]) {
		const source = readFileSync(join(root, page), "utf8");
		assert.match(source, /redirect\(workspaceHomePath\(workspace_id\)\)/, page);
		assert.doesNotMatch(source, /ChatThreadView|[?&]chat=/, page);
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

test("connectors and automations moved into workspace settings; old routes forward", () => {
	const root = process.cwd();
	const shell = readFileSync(
		join(root, "app/dashboard/[workspace_id]/workspace-settings/layout-shell.tsx"),
		"utf8"
	);
	assert.match(shell, /workspace-settings\/connectors/);
	assert.match(shell, /workspace-settings\/automations/);
	for (const [route, target] of [
		["connectors", "workspace-settings/connectors"],
		["automations", "workspace-settings/automations"],
	]) {
		const page = readFileSync(join(root, `app/dashboard/[workspace_id]/${route}/page.tsx`), "utf8");
		assert.ok(page.includes(target), route);
	}
});
