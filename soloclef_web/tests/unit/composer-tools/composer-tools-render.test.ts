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

test("the menu lists all ten platforms and checks the picked one", () => {
	const html = render(
		createElement(
			DropdownMenuPrimitive.Root,
			{ open: true },
			createElement(
				DropdownMenuPrimitive.Content,
				null,
				createElement(ComposerToolsMenuItems, { selected: "reddit", onSelect: noop })
			)
		)
	);
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
	]) {
		assert.match(html, new RegExp(`>${name}<`), name);
	}
	assert.match(html, /Use a tool for the next message/);
	assert.equal(html.match(/lucide-check/g)?.length, 1);
});

test("the chip names the forced tool and offers removal", () => {
	const html = render(createElement(PreferredToolChip, { tool: "tiktok", onRemove: noop }));
	assert.match(html, />TikTok</);
	assert.match(html, /aria-label="Remove TikTok"/);
	assert.match(html, /Your next message will use TikTok/);
});

test("no chip when no tool is picked", () => {
	assert.equal(render(createElement(PreferredToolChip, { tool: null, onRemove: noop })), "");
});

function menuHtml(connected?: ReadonlySet<"exa" | "tavily" | "browserbase">): string {
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

test("Exa, Tavily and Browserbase are listed only where the workspace has their key", () => {
	const none = menuHtml();
	for (const name of ["Exa", "Tavily", "Browserbase"]) {
		assert.doesNotMatch(none, new RegExp(`>${name}<`), name);
	}

	const some = menuHtml(new Set(["exa", "browserbase"] as const));
	assert.match(some, />Exa</);
	assert.match(some, />Browserbase</);
	assert.doesNotMatch(some, />Tavily</);
	// They sit with the general web tools, before the platforms.
	assert.ok(some.indexOf(">Google Search<") < some.indexOf(">Exa<"));
	assert.ok(some.indexOf(">Browserbase<") < some.indexOf(">YouTube<"));
});

test("the chip names a keyed tool too", () => {
	const html = render(createElement(PreferredToolChip, { tool: "tavily", onRemove: noop }));
	assert.match(html, />Tavily</);
	assert.match(html, /Your next message will use Tavily/);
});
