import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { NextIntlClientProvider } from "next-intl";
import { type ComponentProps, createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
	contentSearchQuery,
	passageHref,
	readNotebookFocus,
	snippetParts,
	withoutFocusParams,
} from "@/features/notebooks/lib/content-search";
import {
	type NotebookSearch,
	notebookSearchSchema,
	type SearchPassage,
} from "@/features/notebooks/model/notebook";
import { ContentSearchResults } from "@/features/notebooks/ui/home/content-search-results";

const messagesDir = join(process.cwd(), "messages");
const messages = JSON.parse(readFileSync(join(messagesDir, "en.json"), "utf8"));

function render(element: ReactElement): string {
	const props: ComponentProps<typeof NextIntlClientProvider> = {
		locale: "en",
		messages,
		timeZone: "UTC",
		children: element,
	};
	return renderToStaticMarkup(createElement(NextIntlClientProvider, props));
}

function passage(overrides: Partial<SearchPassage> = {}): SearchPassage {
	return {
		kind: "source",
		title: "Barriers to Asthma Management for School Nurses",
		snippet: "… School nurses play a valuable role in asthma management …",
		highlights: [
			[2, 8],
			[9, 15],
			[40, 46],
		],
		document_id: 152,
		note_id: null,
		source_id: 15,
		folder_name: null,
		document_type: "CRAWLED_URL",
		score: 1.99,
		...overrides,
	};
}

const response: NotebookSearch = notebookSearchSchema.parse({
	query: "school nurses",
	took_ms: 120,
	notebooks: [
		{
			id: 3,
			title: "Studio test run",
			icon: "🩺",
			updated_at: new Date(Date.now() - 3 * 86_400_000 - 60_000).toISOString(),
			passages: [
				passage(),
				passage({
					kind: "note",
					title: "Six barriers",
					snippet: "Lack of time for school nurses",
					highlights: [[17, 23]],
					document_id: null,
					note_id: 7,
					source_id: null,
				}),
				passage({ folder_name: "Guidelines", source_id: 40, title: "GINA.pdf" }),
			],
		},
	],
});

test("only queries of two or more characters are sent, whitespace collapsed", () => {
	assert.equal(contentSearchQuery(""), "");
	assert.equal(contentSearchQuery(" a "), "");
	assert.equal(contentSearchQuery("  school   nurses "), "school nurses");
	assert.equal(contentSearchQuery("ab"), "ab");
});

test("snippet parts follow the highlight ranges and tolerate bad ones", () => {
	const runs = snippetParts("asthma and asthmatics", [
		[11, 17],
		[0, 6],
		[3, 5], // inside an earlier range
		[30, 40], // past the end
		[8, 8], // empty
	]);
	assert.deepEqual(runs, [
		{ text: "asthma", hit: true, start: 0 },
		{ text: " and ", hit: false, start: 6 },
		{ text: "asthma", hit: true, start: 11 },
		{ text: "tics", hit: false, start: 17 },
	]);
	assert.deepEqual(snippetParts("plain", []), [{ text: "plain", hit: false, start: 0 }]);
	assert.equal(
		snippetParts("overlap here", [
			[0, 5],
			[3, 9],
		])
			.map((p) => p.text)
			.join(""),
		"overlap here"
	);
});

test("a passage opens its notebook with the source guide or note named", () => {
	const href = "/dashboard/1/notebooks/3";
	assert.equal(passageHref(href, passage()), `${href}?source=15`);
	assert.equal(
		passageHref(href, passage({ kind: "note", note_id: 7, source_id: null })),
		`${href}?note=7`
	);
	assert.equal(passageHref(href, passage({ source_id: null })), href);
});

test("the notebook page reads, then drops, the focus parameter", () => {
	assert.deepEqual(readNotebookFocus("?source=15"), { kind: "source", id: 15 });
	assert.deepEqual(readNotebookFocus("?tab=4&note=7"), { kind: "note", id: 7 });
	assert.equal(readNotebookFocus("?source=abc"), null);
	assert.equal(readNotebookFocus("?source=-2"), null);
	assert.equal(readNotebookFocus(""), null);
	assert.equal(
		withoutFocusParams("https://notes.example/dashboard/1/notebooks/3?tab=4&source=15"),
		"https://notes.example/dashboard/1/notebooks/3?tab=4"
	);
});

test("results render grouped, highlighted, with a link per passage", () => {
	const html = render(
		createElement(ContentSearchResults, {
			query: "school nurses",
			hits: response.notebooks,
			settled: true,
			failed: false,
			showSpinner: false,
			notebookHref: (id: number) => `/dashboard/1/notebooks/${id}`,
		})
	);
	assert.match(html, /Found inside notebooks/);
	assert.match(html, /Studio test run/);
	assert.match(html, /Updated 3 days ago/);
	assert.match(html, /<mark[^>]*>School<\/mark>/);
	assert.match(html, /<mark[^>]*>nurses<\/mark>/);
	assert.match(html, /href="\/dashboard\/1\/notebooks\/3\?source=15"/);
	assert.match(html, /href="\/dashboard\/1\/notebooks\/3\?note=7"/);
	assert.match(html, /Guidelines\/GINA\.pdf/);
	assert.match(html, /1 notebook with matches/);
	// The notebook link plus one per passage, all reachable with the arrow keys.
	assert.equal(html.match(/data-search-result="/g)?.length, 4);
	assert.doesNotMatch(html, /Nothing inside your notebooks/);
});

test("empty and failed states say so, and nothing claims 'no matches' before results arrive", () => {
	const props = {
		query: "zebra",
		hits: [],
		failed: false,
		showSpinner: false,
		notebookHref: (id: number) => `/n/${id}`,
	};
	const empty = render(createElement(ContentSearchResults, { ...props, settled: true }));
	assert.match(empty, /Nothing inside your notebooks matches “zebra”\./);
	const waiting = render(createElement(ContentSearchResults, { ...props, settled: false }));
	assert.doesNotMatch(waiting, /Nothing inside/);
	const failed = render(
		createElement(ContentSearchResults, { ...props, settled: false, failed: true })
	);
	assert.match(failed, /Couldn’t search inside notebooks/);
});

test("every locale has the home search strings", () => {
	const keys = Object.keys(messages.notebooks).filter((key) => key.startsWith("search_inside_"));
	assert.equal(keys.length, 7);
	for (const locale of ["es", "hi", "ko", "pt", "zh"]) {
		const other = JSON.parse(readFileSync(join(messagesDir, `${locale}.json`), "utf8"));
		for (const key of keys) {
			assert.ok(other.notebooks[key], `${locale} is missing notebooks.${key}`);
			if (key === "search_inside_empty")
				assert.match(other.notebooks[key], /\{query\}/, `${locale} ${key}`);
		}
	}
});
