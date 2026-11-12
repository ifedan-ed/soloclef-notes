import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ParserPicker } from "@/features/parser-choice/ui/parser-picker";
import { resolveParserChoice } from "@/lib/etl/parser-choice";
import { capabilities } from "./parser-capabilities.fixture";

const messages = JSON.parse(readFileSync(join(process.cwd(), "messages", "en.json"), "utf8"));

function render(element: ReactElement): string {
	return renderToStaticMarkup(
		createElement(NextIntlClientProvider, {
			locale: "en",
			messages,
			timeZone: "UTC",
			// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
			children: element,
		})
	);
}

const noop = () => undefined;

/** The markup of one parser's card: its whole <button>, attributes included. */
function card(html: string, parser: string): string {
	const marker = html.indexOf(`data-testid="parser-option-${parser}"`);
	assert.ok(marker >= 0, `${parser} is not listed`);
	const start = html.lastIndexOf("<button", marker);
	return html.slice(start, html.indexOf("</button>", marker));
}

test("the cards list all four parsers and check the one that will be sent", () => {
	const html = render(
		createElement(ParserPicker, {
			variant: "cards",
			status: "ready",
			choice: resolveParserChoice(capabilities(), undefined),
			onChange: noop,
		})
	);
	for (const name of ["Local", "Datalab", "LlamaParse", "Unstructured"]) {
		assert.match(html, new RegExp(`>${name}<`), name);
	}
	assert.match(card(html, "local"), /aria-checked="true"/);
	assert.match(card(html, "llamaparse"), /aria-checked="false"/);
});

test("a parser with no key is shown, says why, and cannot be picked", () => {
	const html = render(
		createElement(ParserPicker, {
			variant: "cards",
			status: "ready",
			choice: resolveParserChoice(capabilities(), undefined),
			onChange: noop,
		})
	);
	const datalab = card(html, "datalab");
	assert.match(datalab, /disabled=""/);
	assert.match(datalab, /Not configured on this server/);
	// Its description is replaced by the reason, not shown beside it.
	assert.doesNotMatch(datalab, /Sends the file to Datalab/);
	// A configured one keeps its description and stays enabled.
	const llama = card(html, "llamaparse");
	assert.doesNotMatch(llama, /disabled=""/);
	assert.match(llama, /Sends the file to LlamaParse/);
});

test("when the server's default is not configured nothing is checked and the person is asked", () => {
	const html = render(
		createElement(ParserPicker, {
			variant: "cards",
			status: "ready",
			choice: resolveParserChoice(capabilities({ default_parser: "datalab" }), undefined),
			onChange: noop,
		})
	);
	assert.doesNotMatch(html, /aria-checked="true"/);
	assert.match(html, /The server&#x27;s usual parser is not configured\. Choose one that is\./);
});

test("with no parser configured at all it says so instead of asking for a pick", () => {
	const none = capabilities();
	const caps = {
		...none,
		parsers: none.parsers.map((parser) => ({
			...parser,
			available: false,
			unavailable_reason: "not_configured",
		})),
	};
	const html = render(
		createElement(ParserPicker, {
			variant: "cards",
			status: "ready",
			choice: resolveParserChoice(caps, undefined),
			onChange: noop,
		})
	);
	assert.match(html, /No parser is configured on this server/);
	assert.doesNotMatch(html, /Choose one that is/);
});

test("loading and a failed load are said in words", () => {
	const empty = resolveParserChoice(undefined, undefined);
	const loading = render(
		createElement(ParserPicker, {
			variant: "select",
			status: "loading",
			choice: empty,
			onChange: noop,
		})
	);
	assert.match(loading, /role="status"/);
	assert.match(loading, /Loading the parsers this server offers/);
	const failed = render(
		createElement(ParserPicker, {
			variant: "select",
			status: "failed",
			choice: empty,
			onChange: noop,
		})
	);
	assert.match(failed, /role="alert"/);
	assert.match(failed, /The parser choices could not be loaded/);
});

test("the compact picker names the parser that will be sent", () => {
	const html = render(
		createElement(ParserPicker, {
			variant: "select",
			status: "ready",
			choice: resolveParserChoice(capabilities(), "llamaparse"),
			onChange: noop,
		})
	);
	assert.match(html, /aria-label="Parser"/);
	assert.match(html, /LlamaParse/);
});

test("a connection that has chosen nothing shows its own wording", () => {
	const html = render(
		createElement(ParserPicker, {
			variant: "select",
			status: "ready",
			choice: { ...resolveParserChoice(capabilities(), undefined), effectiveParser: undefined },
			onChange: noop,
			emptyHint: "No parser chosen yet",
		})
	);
	assert.match(html, /No parser chosen yet/);
	assert.doesNotMatch(html, /usual parser/);
});
