import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import * as DropdownMenuPrimitive from "@radix-ui/react-dropdown-menu";
import { NextIntlClientProvider } from "next-intl";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
	ComposerToolsMenu,
	ComposerToolsMenuItems,
} from "@/features/composer-tools/ui/composer-tools-menu";
import { PreferredToolChip } from "@/features/composer-tools/ui/preferred-tool-chip";

const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));

function render(element: ReactElement): string {
	return renderToStaticMarkup(
		createElement(NextIntlClientProvider, {
			locale: "en",
			messages,
			timeZone: "UTC",
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children
			children: element,
		})
	);
}

const noop = () => undefined;

test("the Tools pill shows its label on desktop and only an icon when compact", () => {
	const desktop = render(createElement(ComposerToolsMenu, { selected: null, onSelect: noop }));
	assert.match(desktop, /aria-label="Pick a tool for the next message"/);
	assert.match(desktop, />Tools</);

	const compact = render(
		createElement(ComposerToolsMenu, { selected: null, onSelect: noop, compact: true })
	);
	assert.doesNotMatch(compact, />Tools</);
	assert.match(compact, /aria-label="Pick a tool for the next message"/);
});

test("the menu lists the literatures and checks the picked one", () => {
	const html = render(
		createElement(
			DropdownMenuPrimitive.Root,
			{ open: true },
			createElement(
				DropdownMenuPrimitive.Content,
				null,
				createElement(ComposerToolsMenuItems, { selected: "arxiv", onSelect: noop })
			)
		)
	);
	for (const name of ["PubMed", "arXiv", "bioRxiv and medRxiv"]) {
		assert.match(html, new RegExp(`>${name}<`), name);
	}
	assert.match(html, /Use a tool for the next message/);
	assert.equal(html.match(/lucide-check/g)?.length, 1);
});

test("the platform scrapers the product cut are not in the menu", () => {
	// Every key connected, so nothing is hidden for want of one.
	const html = menuHtml(new Set(["exa", "tavily"] as const));
	for (const name of [
		"Web",
		"Google Search",
		"YouTube",
		"Reddit",
		"Instagram",
		"TikTok",
		"Google Maps",
		"Indeed",
		"Amazon",
		"Walmart",
		"Browserbase",
	]) {
		assert.doesNotMatch(html, new RegExp(`>${name}<`), name);
	}
	assert.equal(html.match(/role="menuitem"/g)?.length, 5);
});

test("the chip names the forced tool and offers removal", () => {
	const html = render(createElement(PreferredToolChip, { tool: "pubmed", onRemove: noop }));
	assert.match(html, />PubMed</);
	assert.match(html, /aria-label="Remove PubMed"/);
	assert.match(html, /Your next message will use PubMed/);
});

test("no chip when no tool is picked", () => {
	assert.equal(render(createElement(PreferredToolChip, { tool: null, onRemove: noop })), "");
});

function menuHtml(connected?: ReadonlySet<"exa" | "tavily">): string {
	return render(
		createElement(
			DropdownMenuPrimitive.Root,
			{ open: true },
			createElement(
				DropdownMenuPrimitive.Content,
				null,
				createElement(ComposerToolsMenuItems, { selected: null, onSelect: noop, connected })
			)
		)
	);
}

test("Exa and Tavily are listed only where the account has their key", () => {
	const none = menuHtml();
	for (const name of ["Exa", "Tavily"]) {
		assert.doesNotMatch(none, new RegExp(`>${name}<`), name);
	}

	const some = menuHtml(new Set(["exa"] as const));
	assert.match(some, />Exa</);
	assert.doesNotMatch(some, />Tavily</);
	// The web searches sit before the literatures.
	assert.ok(some.indexOf(">Exa<") < some.indexOf(">PubMed<"));
});

test("the chip names a keyed tool too", () => {
	const html = render(createElement(PreferredToolChip, { tool: "tavily", onRemove: noop }));
	assert.match(html, />Tavily</);
	assert.match(html, /Your next message will use Tavily/);
});

test("PubMed, arXiv and the preprint servers are three separate choices, offered without a key", () => {
	// No connected set at all: these need no key, unlike Exa and Tavily.
	const html = menuHtml();
	for (const name of ["PubMed", "arXiv", "bioRxiv and medRxiv"]) {
		assert.match(html, new RegExp(`>${name}<`), name);
	}
	assert.ok(html.indexOf(">PubMed<") < html.indexOf(">arXiv<"));
	assert.ok(html.indexOf(">arXiv<") < html.indexOf(">bioRxiv and medRxiv<"));
});

test("the chip names a literature by its own name", () => {
	const html = render(createElement(PreferredToolChip, { tool: "preprints", onRemove: noop }));
	assert.match(html, />bioRxiv and medRxiv</);
	assert.match(html, /aria-label="Remove bioRxiv and medRxiv"/);
});
