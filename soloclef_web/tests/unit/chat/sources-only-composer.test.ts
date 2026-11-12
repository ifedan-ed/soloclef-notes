import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

// Notebook chats answer from the notebook's sources only (the server forces
// it), so the composer no longer offers a Sources / + Web switch. Web material
// comes in through the research box or, for one message, the Tools menu.

const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
const LOCALES = ["en", "es", "hi", "ko", "pt", "zh"] as const;

test("the desktop composer has no search scope switch", () => {
	const thread = read("../../../components/assistant-ui/thread.tsx");
	assert.ok(!thread.includes("SegmentedControl"));
	assert.ok(!thread.includes("useSearchScopeOptions"));
	assert.ok(!thread.includes("scope_label"));
	// The Tools menu stays.
	assert.ok(thread.includes("<ComposerToolsMenu"));
});

test("the phone + menu has no Search Scope row", () => {
	const drawer = read("../../../components/assistant-ui/composer-add-menu-drawer.tsx");
	assert.ok(!drawer.includes("Search Scope"));
	assert.ok(!drawer.includes("searchScope"));
});

test("the scope switch strings are gone from every locale", () => {
	for (const locale of LOCALES) {
		const messages = JSON.parse(read(`../../../messages/${locale}.json`));
		const scopeKeys = Object.keys(messages.composer_tools).filter((key) =>
			key.startsWith("scope_")
		);
		assert.deepEqual(scopeKeys, [], locale);
	}
});
