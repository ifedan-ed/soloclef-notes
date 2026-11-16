import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NextIntlClientProvider } from "next-intl";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { EtlCapabilities } from "@/contracts/types/document.types";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { capabilities } from "./parser-capabilities.fixture";

// Run with: pnpm exec tsx --test tests/unit/documents/upload-tab-vision.test.ts

const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));

/** The Upload tab, before anything is picked, with the server's capabilities known. */
async function renderEmptyTab(served: EtlCapabilities): Promise<string> {
	// Imported here: the module reads the API's base address when loaded.
	const { DocumentUploadTab } = await import("@/components/sources/DocumentUploadTab");
	const queryClient = new QueryClient();
	queryClient.setQueryData(cacheKeys.etl.capabilities(), served);
	return renderToStaticMarkup(
		createElement(QueryClientProvider, {
			client: queryClient,
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
			children: createElement(NextIntlClientProvider, {
				locale: "en",
				messages,
				timeZone: "UTC",
				// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
				children: createElement(DocumentUploadTab, {}),
			}),
		})
	);
}

test("the Vision switch is offered before any file is picked", async () => {
	// Pictures are accepted only with Vision on. When the switch showed only
	// beside picked files, a person who picked only pictures had them all
	// refused and never saw a way to turn it on.
	const html = await renderEmptyTab(capabilities({ vision_available: true }));
	assert.match(
		html,
		/role="switch"[^>]*aria-label="Enable vision LLM"|aria-label="Enable vision LLM"[^>]*role="switch"/
	);
});

test("without a vision model there is no switch", async () => {
	const html = await renderEmptyTab(capabilities({ vision_available: false }));
	assert.doesNotMatch(html, /Enable vision LLM/);
});
