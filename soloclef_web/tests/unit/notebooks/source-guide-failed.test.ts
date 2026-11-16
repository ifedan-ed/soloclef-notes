import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NextIntlClientProvider } from "next-intl";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

// Run with: pnpm exec tsx --test tests/unit/notebooks/source-guide-failed.test.ts

const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));

async function renderFailure(documentId: number, reason: string | null): Promise<string> {
	// Imported here: the module reads the API's base address when loaded.
	const { SourceFailure } = await import("@/features/notebooks/ui/sources/source-failure");
	const queryClient = new QueryClient();
	queryClient.setQueryData(["documents", "failure", documentId], reason);
	return renderToStaticMarkup(
		createElement(QueryClientProvider, {
			client: queryClient,
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
			children: createElement(NextIntlClientProvider, {
				locale: "en",
				messages,
				timeZone: "UTC",
				// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
				children: createElement(SourceFailure, { documentId }),
			}),
		})
	);
}

test("a source that could not be read says so, with the server's reason", async () => {
	const reason = "The local parser: no page has any text. This looks like a scan.";
	const html = await renderFailure(74, reason);
	assert.match(html, /This source couldn&#x27;t be read\./);
	assert.match(html, /This looks like a scan\./);
});

test("with the reason not read yet, it still says the source failed", async () => {
	const html = await renderFailure(75, null);
	assert.match(html, /This source couldn&#x27;t be read\./);
});

test("the guide view puts the failure where a failed source's guide would be", () => {
	// Rendered through SourceFailure; the guide is not asked for (it never comes).
	const view = readFileSync(
		join(process.cwd(), "features", "notebooks", "ui", "sources", "source-guide.tsx"),
		"utf8"
	);
	assert.match(view, /useSourceGuide\(notebookId, failed \? null : source\.id\)/);
	assert.match(view, /<SourceFailure documentId=\{source\.target_id\} \/>/);
});
