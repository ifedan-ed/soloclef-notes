import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { type ComponentProps, createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { NeedsSourcesBody } from "@/features/notebooks/ui/studio/generate-dialog";

const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));

test("a tile opened with no sources explains why and offers both ways to add some", () => {
	const body: ReactElement = createElement(NeedsSourcesBody, {
		kind: "mindmap",
		onAddSources: () => undefined,
		onResearch: () => undefined,
	});
	const props: ComponentProps<typeof NextIntlClientProvider> = {
		locale: "en",
		messages,
		timeZone: "UTC",
		children: body,
	};
	const html = renderToStaticMarkup(createElement(NextIntlClientProvider, props));
	assert.match(html, /Mind Map is made from your notebook&#x27;s sources/);
	assert.match(html, /Add sources/);
	assert.match(html, /Research the web/);
});
