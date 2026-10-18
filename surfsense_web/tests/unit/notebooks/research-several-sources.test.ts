import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
	canTick,
	initialSourcePicks,
	MAX_RESEARCH_SOURCES,
	parseSourcePicks,
	pickOnly,
	researchRequestFor,
	type SourcePicks,
	searchedSources,
	setSeveral,
	toggleSource,
	withoutUnavailable,
} from "@/features/notebooks/lib/research-source-picks";
import { parseResearchEvent } from "@/features/notebooks/lib/research-sse";
import { idleResearch, researchReducer } from "@/features/notebooks/lib/research-state";
import type { ResearchRequest, ResearchResult } from "@/features/notebooks/model/research";
import { READ_PAGE_ID } from "@/features/notebooks/model/research-sources";
import { ResearchForm } from "@/features/notebooks/ui/sources/research/research-form";
import { ResearchProgress } from "@/features/notebooks/ui/sources/research/research-progress";
import { ResearchResults } from "@/features/notebooks/ui/sources/research/research-results";

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

function several(...sources: SourcePicks["sources"]): SourcePicks {
	return { source: sources[0], several: true, sources };
}

// --- picking -----------------------------------------------------------------------

test("one click picks one source and leaves several mode", () => {
	const picked = pickOnly(several("web", "reddit"), "pubmed");
	assert.deepEqual(picked, { source: "pubmed", several: false, sources: ["pubmed"] });
	assert.deepEqual(searchedSources(picked), ["pubmed"]);
});

test("reading a page searches nothing and keeps the last sources for later", () => {
	const reading = pickOnly(several("web", "reddit"), READ_PAGE_ID);
	assert.equal(reading.source, READ_PAGE_ID);
	assert.equal(reading.several, false);
	assert.deepEqual(searchedSources(reading), []);
	assert.equal(researchRequestFor("q", reading, "fast"), null);
});

test("turning several on starts from the current source; off keeps the first", () => {
	const on = setSeveral(pickOnly(initialSourcePicks, "arxiv"), true);
	assert.deepEqual(on, { source: "arxiv", several: true, sources: ["arxiv"] });
	assert.deepEqual(setSeveral(several("reddit", "web"), false), {
		source: "reddit",
		several: false,
		sources: ["reddit"],
	});
	const fromPage = setSeveral(pickOnly(initialSourcePicks, READ_PAGE_ID), true);
	assert.deepEqual(fromPage.sources, ["web"]);
});

test("ticking adds up to the cap, unticking keeps at least one", () => {
	let picks = setSeveral(initialSourcePicks, true);
	for (const id of ["reddit", "pubmed", "arxiv"] as const) picks = toggleSource(picks, id);
	assert.equal(picks.sources.length, MAX_RESEARCH_SOURCES);
	assert.equal(MAX_RESEARCH_SOURCES, 4);
	assert.equal(canTick(picks, "youtube"), false);
	assert.equal(toggleSource(picks, "youtube"), picks, "a fifth tick is refused");
	assert.equal(canTick(picks, "reddit"), true);

	picks = toggleSource(picks, "web");
	assert.deepEqual(picks.sources, ["reddit", "pubmed", "arxiv"]);
	assert.equal(picks.source, "reddit", "the first ticked leads");
	const alone = several("pubmed");
	assert.equal(toggleSource(alone, "pubmed"), alone, "the last one stays ticked");
});

test("the request names several sources only when there are several", () => {
	assert.deepEqual(researchRequestFor("iron", several("web", "pubmed"), "deep"), {
		query: "iron",
		source: "web",
		sources: ["web", "pubmed"],
		mode: "deep",
	});
	assert.deepEqual(researchRequestFor("iron", several("pubmed"), "fast"), {
		query: "iron",
		source: "pubmed",
		mode: "fast",
	});
	assert.deepEqual(researchRequestFor("iron", pickOnly(initialSourcePicks, "exa"), "fast"), {
		query: "iron",
		source: "exa",
		mode: "fast",
	});
});

test("sources that became unavailable are dropped; nothing left falls back to Web", () => {
	assert.deepEqual(withoutUnavailable(several("exa", "pubmed"), { exa: "not_connected" }), {
		source: "pubmed",
		several: true,
		sources: ["pubmed"],
	});
	assert.deepEqual(
		withoutUnavailable(pickOnly(initialSourcePicks, "tavily"), { tavily: "not_connected" }),
		{ source: "web", several: false, sources: ["web"] }
	);
	const kept = several("web", "reddit");
	assert.equal(withoutUnavailable(kept, {}), kept);
});

test("remembered picks are read back, cleaned, or refused", () => {
	assert.deepEqual(parseSourcePicks(several("web", "reddit")), several("web", "reddit"));
	assert.deepEqual(
		parseSourcePicks({
			source: "web",
			several: true,
			sources: ["web", "bing", "web", "reddit", "arxiv", "pubmed", "youtube"],
		}),
		several("web", "reddit", "arxiv", "pubmed")
	);
	assert.deepEqual(parseSourcePicks({ source: "youtube", several: false, sources: ["web"] }), {
		source: "youtube",
		several: false,
		sources: ["youtube"],
	});
	assert.equal(parseSourcePicks({ source: "bing", sources: ["web"] }), null);
	assert.equal(parseSourcePicks({ source: "web", sources: [] }), null);
	assert.equal(parseSourcePicks("web"), null);
	assert.equal(parseSourcePicks(null), null);
});

// --- the stream --------------------------------------------------------------------

const request: ResearchRequest = {
	query: "iron",
	source: "web",
	sources: ["web", "reddit"],
	mode: "fast",
};

function result(id: string, source: string): ResearchResult {
	return { id, url: `https://example.com/${id}`, title: `Title ${id}`, snippet: "", source };
}

test("provider events are parsed and tracked per source, in arrival order", () => {
	const events = [
		'{"type":"provider","source":"web","status":"searching"}',
		'{"type":"provider","source":"reddit","status":"searching"}',
		'{"type":"provider","source":"reddit","status":"error","code":"source_busy","message":"busy"}',
		'{"type":"provider","source":"web","status":"done","count":2}',
	].map((line) => parseResearchEvent(line));
	assert.ok(events.every(Boolean));
	assert.equal(parseResearchEvent('{"type":"provider","source":"web","status":"lost"}'), null);

	let state = researchReducer(idleResearch, { type: "start", request });
	for (const event of events) if (event) state = researchReducer(state, { type: "event", event });
	state = researchReducer(state, {
		type: "event",
		event: { type: "results", results: [result("a", "web")] },
	});
	state = researchReducer(state, { type: "event", event: { type: "done" } });
	assert.equal(state.status, "results");
	assert.deepEqual(state.status === "results" && state.providers, [
		{ source: "web", status: "done", count: 2, code: null, message: null },
		{ source: "reddit", status: "error", count: null, code: "source_busy", message: "busy" },
	]);
});

test("when every source fails the error names the source it is worded after", () => {
	const searching = researchReducer(idleResearch, { type: "start", request });
	const failed = researchReducer(searching, {
		type: "event",
		event: { type: "error", code: "source_not_connected", message: "x", source: "exa" },
	});
	assert.equal(failed.status === "error" && failed.source, "exa");
});

// --- rendering ------------------------------------------------------------------------

test("the source chip shows how many sources are picked and names them all", () => {
	const html = render(
		createElement(ResearchForm, {
			draft: { query: "", picks: several("web", "reddit", "pubmed"), mode: "fast" },
			onDraftChange: noop,
			onSubmit: noop,
		})
	);
	assert.match(html, /aria-label="Search in: Web, Reddit, PubMed"/);
	assert.match(html, />3 sources</);
});

test("a single pick still reads as that one source", () => {
	const html = render(
		createElement(ResearchForm, {
			draft: { query: "", picks: several("reddit"), mode: "fast" },
			onDraftChange: noop,
			onSubmit: noop,
		})
	);
	assert.match(html, /aria-label="Search in: Reddit"/);
	assert.doesNotMatch(html, /sources</);
});

test("a running several-source search shows how each source is getting on", () => {
	const html = render(
		createElement(ResearchProgress, {
			phase: "searching",
			progress: 20,
			providers: [
				{ source: "web", status: "done", count: 7, code: null, message: null },
				{ source: "reddit", status: "searching", count: null, code: null, message: null },
				{ source: "pubmed", status: "error", count: null, code: "source_busy", message: null },
			],
			onCancel: noop,
		})
	);
	assert.match(html, /Web: 7 found/);
	assert.match(html, /Reddit: searching/);
	assert.match(html, /PubMed: didn&#x27;t answer/);
});

test("merged results name their source and list the sources that did not answer", () => {
	const html = render(
		createElement(ResearchResults, {
			results: [result("a", "web"), result("b", "pubmed")],
			source: "web",
			several: true,
			providers: [
				{ source: "web", status: "done", count: 1, code: null, message: null },
				{ source: "pubmed", status: "done", count: 1, code: null, message: null },
				{ source: "reddit", status: "error", count: null, code: "source_timeout", message: null },
			],
			importing: false,
			importFailed: false,
			onImport: noop,
			onDismiss: noop,
		})
	);
	assert.match(html, /From Web/);
	assert.match(html, /From PubMed/);
	assert.match(html, /Not every source answered:/);
	assert.match(html, /Reddit took too long to answer/);
});

test("results of one source carry no source badges", () => {
	const html = render(
		createElement(ResearchResults, {
			results: [result("a", "web")],
			source: "web",
			importing: false,
			importFailed: false,
			onImport: noop,
			onDismiss: noop,
		})
	);
	assert.doesNotMatch(html, /From Web/);
	assert.doesNotMatch(html, /Not every source answered/);
});
