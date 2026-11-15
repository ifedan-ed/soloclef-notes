import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { buildTurnRenderItems } from "@/features/chat-messages/timeline/grouping";
import {
	DRAW_FIGURE_TOOL,
	figureImage,
	parseFigureCard,
	svgDataUrl,
} from "@/features/figures/figure-card";
import { DrawFigureToolUI } from "@/features/figures/ui/figure-card";

// Run with: pnpm exec tsx --test tests/unit/figures/figure-card.test.ts

const light =
	'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 312.5" width="400" height="312.5"><rect width="400" height="312.5" fill="#ffffff"/><text x="14" y="28" fill="#1d232b">Fast breathing &amp; age</text></svg>';
const dark = light.replace("#ffffff", "#15191e").replace("#1d232b", "#e6e9ee");

/** A finished call's card, in the shape the server sends (figures-go through soloclef_api). */
function card(overrides: Record<string, unknown> = {}) {
	return {
		type: "bar_chart",
		title: "WHO fast-breathing thresholds by age",
		alt: "Bar chart: 60 breaths a minute under 2 months, 50 to 11 months, 40 to 5 years.",
		caption: "WHO IMCI thresholds for fast breathing.",
		width: 400,
		height: 312.5,
		svg: light,
		svg_dark: dark,
		...overrides,
	};
}

type Props = Parameters<typeof DrawFigureToolUI>[0];
const render = (result: unknown) =>
	renderToStaticMarkup(createElement(DrawFigureToolUI, { result } as unknown as Props));

test("the server's card parses, with the caption absent", () => {
	const { caption: _caption, ...noCaption } = card();
	const data = parseFigureCard(noCaption);
	assert.equal(data?.type, "bar_chart");
	assert.equal(data?.caption, undefined);
});

test("a running, refused or foreign result is not a figure", () => {
	assert.equal(parseFigureCard(undefined), null);
	assert.equal(parseFigureCard("drawn"), null);
	// The server's refusal, which the model corrects and calls again from.
	assert.equal(parseFigureCard({ error: "spec.bars[2].value: a number is needed" }), null);
	assert.equal(parseFigureCard(card({ svg_dark: undefined })), null);
	assert.equal(parseFigureCard(card({ height: 0 })), null);
	// Only an SVG document is drawn: never a URL or markup of another kind.
	assert.equal(parseFigureCard(card({ svg: "https://example.com/x.svg" })), null);
});

test("a result is parsed and encoded once, however often it is drawn", () => {
	const result = card();
	const data = parseFigureCard(result);
	assert.ok(data);
	assert.equal(parseFigureCard(result), data);
	assert.equal(figureImage(data), figureImage(data));
	// A refusal is remembered too, so it is not parsed again on every token.
	const refused = { error: "no" };
	assert.equal(parseFigureCard(refused), null);
	assert.equal(parseFigureCard(refused), null);
});

test("the image URLs are the SVG documents as they came, with the figure's shape", () => {
	const data = parseFigureCard(card());
	assert.ok(data);
	const image = figureImage(data);
	assert.equal(image.aspectRatio, 400 / 312.5);
	assert.ok(image.light.startsWith("data:image/svg+xml;charset=utf-8,"));
	assert.equal(decodeURIComponent(image.light.split(",").slice(1).join(",")), light);
	assert.equal(image.dark, svgDataUrl(dark));
});

test("the card draws both versions as images, never as markup, in a box of the figure's shape", () => {
	const html = render(card());
	assert.equal(html.match(/<img /g)?.length, 2);
	// The SVG is only ever inside an image URL; no <svg> or <text> reaches the page.
	assert.doesNotMatch(html, /<svg|<text/);
	assert.match(html, /aspect-ratio:1\.28/);
	assert.match(html, /max-w-\[560px\]/);
	assert.match(html, /alt="Bar chart: 60 breaths/);
	// One for light pages, one for dark; CSS shows the page's own.
	assert.match(html, /class="block size-full dark:hidden"/);
	assert.match(html, /class="hidden size-full dark:block"/);
});

test("nothing is drawn for a call that has no figure", () => {
	assert.equal(render(undefined), "");
	assert.equal(render({ error: "spec.nodes: at least two are needed" }), "");
});

test("a drawn figure is in the answer; one being drawn or refused stays in the trace", () => {
	const names = new Set([DRAW_FIGURE_TOOL]);
	const kinds = (result: unknown) =>
		buildTurnRenderItems({
			parts: [
				{ type: "tool-call", toolName: DRAW_FIGURE_TOOL, toolCallId: "call-1", result },
				{ type: "text", text: "Fast breathing depends on age." },
			],
			bodyToolNames: names,
			showReasoning: true,
			threadRunning: false,
			timingStatus: "completed",
		}).map((item) => item.kind);

	// The leading segment is the turn's header, there with or without steps.
	assert.deepEqual(kinds(card()), ["segment", "body-tool", "text"]);
	assert.deepEqual(kinds(undefined), ["segment", "text"]);
	assert.deepEqual(kinds({ error: "spec.bars: at least one is needed" }), ["segment", "text"]);
});
