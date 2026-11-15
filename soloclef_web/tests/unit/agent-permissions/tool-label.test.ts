import assert from "node:assert/strict";
import { test } from "node:test";
import { toolLabel } from "@/features/agent-permissions/lib/tool-label";

test("a tool's snake_case name reads as a sentence", () => {
	for (const [name, label] of [
		["search_google_drive", "Search google drive"],
		["read_email", "Read email"],
		["create_note", "Create note"],
		["mcp:search", "Mcp search"],
		["Delete_Folder", "Delete folder"],
	] as const) {
		assert.equal(toolLabel(name), label, name);
	}
});

test("a name of separators only is shown as it is, never as nothing", () => {
	assert.equal(toolLabel("__"), "__");
	assert.equal(toolLabel(""), "");
});
