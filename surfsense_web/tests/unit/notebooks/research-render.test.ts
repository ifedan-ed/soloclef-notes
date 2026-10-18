import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { initialSourcePicks, pickOnly } from "@/features/notebooks/lib/research-source-picks";
import type { ResearchResult } from "@/features/notebooks/model/research";
import { ResearchError } from "@/features/notebooks/ui/sources/research/research-error";
import { ResearchForm } from "@/features/notebooks/ui/sources/research/research-form";
import { ResearchProgress } from "@/features/notebooks/ui/sources/research/research-progress";
import { ResearchResults } from "@/features/notebooks/ui/sources/research/research-results";
import { SourcesEmptyState } from "@/features/notebooks/ui/sources/sources-empty-state";

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

test("an empty notebook explains sources and invites a drop", () => {
	const html = render(createElement(SourcesEmptyState, { onBrowseFiles: noop }));
	assert.match(html, /Saved sources will appear here/);
	assert.match(
		html,
		/Add files, websites, or more. Then ask questions or create things based on these sources./
	);
	assert.match(html, /Drop files here or add a source/);
});

test("the research box defaults to Web and Fast research", () => {
	const html = render(
		createElement(ResearchForm, {
			draft: { query: "", picks: pickOnly(initialSourcePicks, "web"), mode: "fast" },
			onDraftChange: noop,
			onSubmit: noop,
		})
	);
	assert.match(html, /placeholder="Search the web for new sources"/);
	assert.match(html, /aria-label="Search in: Web"/);
	assert.match(html, /aria-label="Research mode: Fast research"/);
	assert.match(html, /aria-label="Search"[^>]*disabled|disabled[^>]*aria-label="Search"/);
});

test("the research box names the chosen tool and mode", () => {
	const html = render(
		createElement(ResearchForm, {
			draft: { query: "iron", picks: pickOnly(initialSourcePicks, "youtube"), mode: "deep" },
			onDraftChange: noop,
			onSubmit: noop,
		})
	);
	assert.match(html, />YouTube</);
	assert.match(html, />Deep research</);
	assert.doesNotMatch(html, /disabled=""/);
});

test("a running search shows its phase and progress", () => {
	const html = render(
		createElement(ResearchProgress, { phase: "reading", progress: 40, onCancel: noop })
	);
	assert.match(html, /Reading sources…/);
	assert.match(html, /aria-valuenow="40"/);
	assert.match(html, />Cancel</);
});

test("results list links out in a new tab and offers to add the checked ones", () => {
	const results: ResearchResult[] = [
		{
			id: "a",
			url: "https://example.com/a",
			title: "Iron basics",
			snippet: "What iron does",
			source: "web",
			meta: "example.com · 2025",
		},
		{
			id: "b",
			url: "https://youtube.com/watch?v=b",
			title: "Iron video",
			snippet: "",
			source: "youtube",
		},
	];
	const html = render(
		createElement(ResearchResults, {
			results,
			source: "web",
			importing: false,
			importFailed: false,
			onImport: noop,
			onDismiss: noop,
		})
	);
	assert.match(html, /2 sources found/);
	assert.match(
		html,
		/<a href="https:\/\/example.com\/a" target="_blank" rel="noopener noreferrer"/
	);
	assert.match(html, /example.com · 2025/);
	assert.match(html, />Select all</);
	assert.match(html, /Add 2 sources/);
	assert.match(html, /aria-label="Dismiss"/);
	assert.equal((html.match(/aria-checked="true"/g) ?? []).length, 3, "both rows and select-all");
});

test("an empty result set says so; a failed import offers to retry", () => {
	assert.match(
		render(
			createElement(ResearchResults, {
				results: [],
				source: "web",
				importing: false,
				importFailed: false,
				onImport: noop,
				onDismiss: noop,
			})
		),
		/No sources found.*Nothing matched/
	);
	const failed = render(
		createElement(ResearchResults, {
			results: [{ id: "a", url: "https://a.test", title: "A", snippet: "", source: "reddit" }],
			source: "reddit",
			importing: false,
			importFailed: true,
			onImport: noop,
			onDismiss: noop,
		})
	);
	assert.match(failed, /Couldn(’|'|&#x27;)t add the selected sources/);
	assert.match(failed, /Try adding 1 source again/);
});

test("every result gets a fixed-size tile; images are proxied, lazy and referrer-free", () => {
	const html = render(
		createElement(ResearchResults, {
			results: [
				{
					id: "v",
					url: "https://youtube.com/watch?v=v",
					title: "Video",
					snippet: "",
					source: "youtube",
					thumbnail_url: "/api/v1/research/thumbnails?u=abc&s=def",
				},
				{
					id: "w",
					url: "https://nhs.uk/a",
					title: "Page",
					snippet: "",
					source: "web",
					favicon_url: "/api/v1/research/thumbnails?u=fav&s=sig",
				},
				{ id: "r", url: "https://reddit.com/r/a", title: "Post", snippet: "", source: "reddit" },
			],
			source: "web",
			importing: false,
			importFailed: false,
			onImport: noop,
			onDismiss: noop,
		})
	);
	assert.equal((html.match(/class="relative flex size-12 shrink-0/g) ?? []).length, 3);
	const images = html.match(/<img [^>]*>/g) ?? [];
	// Two proxied images plus the source marks drawn under them.
	const proxied = images.filter((tag) => tag.includes("/api/v1/research/thumbnails"));
	assert.equal(proxied.length, 2);
	for (const tag of proxied) {
		assert.match(tag, /loading="lazy"/);
		assert.match(tag, /referrerPolicy="no-referrer"|referrerpolicy="no-referrer"/);
		assert.match(tag, /width="\d+"/);
	}
	assert.ok(!html.includes("i.ytimg.com"));
});

test("a paper shows its source badges and an open-access mark", () => {
	const html = render(
		createElement(ResearchResults, {
			results: [
				{
					id: "p",
					url: "https://pubmed.ncbi.nlm.nih.gov/35721052/",
					title: "HFNC in children",
					snippet: "",
					source: "pubmed",
					meta: "Milési C et al. · Front Med · 2022",
					paper: {
						pmid: "35721052",
						authors: [],
						categories: [],
						badges: ["pubmed", "pmc", "future-index"],
						open_access: true,
					},
				},
			],
			source: "pubmed",
			importing: false,
			importFailed: false,
			onImport: noop,
			onDismiss: noop,
		})
	);
	assert.match(html, />PubMed</);
	assert.match(html, />PMC</);
	assert.match(html, />Open access</);
	assert.doesNotMatch(html, /future-index/);
});

test("TikTok coming back empty explains why", () => {
	const html = render(
		createElement(ResearchResults, {
			results: [],
			source: "tiktok",
			importing: false,
			importFailed: false,
			onImport: noop,
			onDismiss: noop,
		})
	);
	assert.match(html, /TikTok had no videos under hashtags from this query/);
});

test("a known failure is worded in the user's language, naming the source", () => {
	const html = render(
		createElement(ResearchError, {
			message: "raw english",
			code: "source_blocked",
			source: "google_scholar",
			onRetry: noop,
			onDismiss: noop,
		})
	);
	assert.match(html, /Google Scholar is blocking automated searches right now/);
	assert.doesNotMatch(html, /raw english/);
	const unknown = render(
		createElement(ResearchError, {
			message: "server said this",
			code: "brand_new_code",
			source: "web",
			onRetry: noop,
			onDismiss: noop,
		})
	);
	assert.match(unknown, /server said this/);
});

test("the research box can search the academic sources", () => {
	const html = render(
		createElement(ResearchForm, {
			draft: { query: "hfnc", picks: pickOnly(initialSourcePicks, "pubmed"), mode: "deep" },
			onDraftChange: noop,
			onSubmit: noop,
		})
	);
	assert.match(html, /aria-label="Search in: PubMed"/);
	assert.match(html, /src="\/connectors\/pubmed.svg"|pubmed.svg/);
});

test("the research box can search Exa and Tavily, with their marks", () => {
	for (const [source, name] of [
		["exa", "Exa"],
		["tavily", "Tavily"],
	] as const) {
		const html = render(
			createElement(ResearchForm, {
				draft: { query: "asthma", picks: pickOnly(initialSourcePicks, source), mode: "fast" },
				onDraftChange: noop,
				onSubmit: noop,
				unavailable: {},
			})
		);
		assert.match(html, new RegExp(`aria-label="Search in: ${name}"`));
		assert.match(html, new RegExp(`${source}.svg`));
	}
});

test("a missing, refused or exhausted Exa/Tavily key is worded for the user", () => {
	const cases = [
		["source_not_connected", /Exa isn&#x27;t connected in this workspace/],
		["source_key_rejected", /Exa refused this workspace&#x27;s API key/],
		["source_quota", /Exa turned the search down/],
	] as const;
	for (const [code, wording] of cases) {
		const html = render(
			createElement(ResearchError, {
				message: "raw english",
				code,
				source: "exa",
				onRetry: noop,
				onDismiss: noop,
			})
		);
		assert.match(html, wording);
		assert.doesNotMatch(html, /raw english/);
	}
});
