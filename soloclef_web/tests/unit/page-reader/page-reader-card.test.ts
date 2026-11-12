import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { buildTurnRenderItems } from "@/features/chat-messages/timeline/grouping";
import {
	pageAddress,
	pageReaderSummary,
	parsePageReaderCard,
} from "@/features/page-reader/page-reader-card";
import { PageReaderCard, PageReaderToolUI } from "@/features/page-reader/ui/page-reader-card";

// Run with: pnpm exec tsx --test tests/unit/page-reader/page-reader-card.test.ts

/** A finished call's payload, as soloclef_api/internal/chat/pagereader/card.go writes it. */
const payload = {
	status: "completed",
	pages: [
		{
			url: "https://www.example.org/guides/asthma?page=2",
			status: "success",
			content_preview: "Asthma in children\n\nWheeze is common in the first years…",
		},
		{
			url: "https://app.example.com/dashboard",
			status: "empty",
			error: "The page has scripts and no text. It is probably drawn by JavaScript.",
		},
		{ url: "https://gone.example.net/", status: "failed", error: "The server answered 404." },
	],
	succeeded: 1,
	total: 3,
};

test("the server's payload parses, with the optional fields absent", () => {
	const card = parsePageReaderCard(payload);
	assert.equal(card?.pages.length, 3);
	assert.equal(card?.pages[0].error, undefined);
	assert.equal(card?.pages[2].content_preview, undefined);
});

test("anything that is not a finished card is not one", () => {
	// Running, waiting for approval, failed as a whole, or another tool's result.
	for (const result of [
		undefined,
		null,
		"Read 2 of 3 pages.",
		{ status: "completed", result_length: 1200 },
		{ __interrupt__: true, action_requests: [] },
		{ ...payload, status: "running" },
		{ ...payload, pages: [] },
		{ ...payload, pages: [{ url: "https://example.org", status: "skipped" }] },
	]) {
		assert.equal(parsePageReaderCard(result), null, JSON.stringify(result));
	}
});

test("the summary counts what was read against what was asked", () => {
	const card = parsePageReaderCard(payload);
	assert.ok(card);
	assert.equal(pageReaderSummary(card), "Read 1 of 3 pages");
	assert.equal(pageReaderSummary({ ...card, succeeded: 3 }), "Read 3 pages");
	assert.equal(pageReaderSummary({ ...card, succeeded: 1, total: 1 }), "Read 1 page");
	assert.equal(pageReaderSummary({ ...card, succeeded: 0 }), "Could not read 3 pages");
	assert.equal(pageReaderSummary({ ...card, succeeded: 0, total: 1 }), "Could not read the page");
});

test("an address is split into its site and the rest", () => {
	assert.deepEqual(pageAddress("https://www.example.org/guides/asthma?page=2"), {
		host: "example.org",
		rest: "/guides/asthma?page=2",
		href: "https://www.example.org/guides/asthma?page=2",
	});
	assert.equal(pageAddress("https://example.org/").rest, "");
});

test("only http and https addresses become links", () => {
	for (const url of [
		"javascript:alert(1)",
		"file:///etc/passwd",
		"data:text/html,x",
		"not a url",
	]) {
		const address = pageAddress(url);
		assert.equal(address.href, null, url);
		assert.equal(address.host, url);
	}
});

test("the card names every page, how it fared, and why a page has no text", () => {
	const card = parsePageReaderCard(payload);
	assert.ok(card);
	const html = renderToStaticMarkup(createElement(PageReaderCard, { data: card }));
	assert.match(html, /Read 1 of 3 pages/);
	for (const host of ["example.org", "app.example.com", "gone.example.net"]) {
		assert.match(html, new RegExp(`>${host.replace(/\./g, "\\.")}<`), host);
	}
	assert.match(html, /The page has scripts and no text/);
	assert.match(html, /The server answered 404\./);
	assert.match(html, /aria-label="Read"/);
	assert.match(html, /aria-label="No text"/);
	assert.match(html, /aria-label="Not read"/);
	// Links to the pages open elsewhere and hand over no opener.
	assert.match(html, /href="https:\/\/gone\.example\.net\/"[^>]*rel="noopener noreferrer"/);
});

test("a page's text is shown as text: markup in a preview is escaped", () => {
	const card = parsePageReaderCard({
		status: "completed",
		pages: [
			{
				url: "javascript:alert(1)",
				status: "failed",
				error: '<img src=x onerror="alert(1)">',
			},
		],
		succeeded: 0,
		total: 1,
	});
	assert.ok(card);
	const html = renderToStaticMarkup(createElement(PageReaderCard, { data: card }));
	assert.doesNotMatch(html, /<img/);
	assert.doesNotMatch(html, /href="javascript:/);
	assert.match(html, /Could not read the page/);
});

test("the tool component draws nothing for a result that is not a card", () => {
	const props = { result: { status: "completed", result_length: 900 } } as unknown as Parameters<
		typeof PageReaderToolUI
	>[0];
	assert.equal(renderToStaticMarkup(createElement(PageReaderToolUI, props)), "");
});

test("a finished page read is a card in the answer; an unfinished one stays in the trace", () => {
	const names = new Set(["web_crawl"]);
	const call = (result: unknown) => [
		{ type: "tool-call", toolName: "web_crawl", toolCallId: "call-1", result },
		{ type: "text", text: "Here is what the pages say." },
	];
	const kinds = (result: unknown) =>
		buildTurnRenderItems({
			parts: call(result),
			bodyToolNames: names,
			showReasoning: true,
			threadRunning: false,
			timingStatus: "completed",
		}).map((item) => item.kind);

	assert.deepEqual(kinds(payload), ["segment", "body-tool", "text"]);
	assert.deepEqual(kinds(undefined), ["segment", "text"]);
	assert.deepEqual(kinds({ status: "completed", result_length: 900 }), ["segment", "text"]);
});
