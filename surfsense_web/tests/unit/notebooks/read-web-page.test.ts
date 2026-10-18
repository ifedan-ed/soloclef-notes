import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { researchSourcesStatusSchema } from "@/features/notebooks/model/research";
import {
	isPageUrl,
	isResearchBoxSource,
	isResearchSourceId,
	READ_PAGE_ID,
	WEB_GROUP_MENU_IDS,
	WEB_GROUP_SOURCE_IDS,
} from "@/features/notebooks/model/research-sources";

const LOCALES = ["en", "es", "hi", "ko", "pt", "zh"] as const;
const PAGE_KEYS = [
	"source_page",
	"page_placeholder",
	"page_submit",
	"page_via_browserbase",
	"page_adding",
	"page_added",
	"page_hint_browserbase",
	"page_hint_crawler",
	"page_failed",
];

test("Read a web page sits right after Exa and Tavily in the web group", () => {
	assert.deepEqual(WEB_GROUP_MENU_IDS.slice(0, 5), [
		"web",
		"google_search",
		"exa",
		"tavily",
		READ_PAGE_ID,
	]);
	assert.deepEqual(
		WEB_GROUP_MENU_IDS.filter((id) => id !== READ_PAGE_ID),
		[...WEB_GROUP_SOURCE_IDS]
	);
});

test("reading a page is a box choice, never a search source", () => {
	assert.equal(isResearchBoxSource(READ_PAGE_ID), true);
	assert.equal(isResearchSourceId(READ_PAGE_ID), false);
	assert.equal(isResearchBoxSource("exa"), true);
	assert.equal(isResearchBoxSource("gopher"), false);
});

test("only http and https links can be submitted", () => {
	assert.equal(isPageUrl("https://example.org/report"), true);
	assert.equal(isPageUrl("  http://example.com/a?b=c  "), true);
	assert.equal(isPageUrl("example.org/page"), false);
	assert.equal(isPageUrl("javascript:alert(1)"), false);
	assert.equal(isPageUrl("file:///etc/passwd"), false);
	assert.equal(isPageUrl(""), false);
	assert.equal(isPageUrl(`https://example.com/${"a".repeat(2100)}`), false);
});

test("the status says what reads a page, and defaults to the crawler", () => {
	assert.equal(researchSourcesStatusSchema.parse({ unavailable: {} }).page_reader, "crawler");
	assert.equal(
		researchSourcesStatusSchema.parse({ unavailable: {}, page_reader: "browserbase" }).page_reader,
		"browserbase"
	);
});

test("every locale words Read a web page", () => {
	for (const locale of LOCALES) {
		const catalogue = JSON.parse(
			readFileSync(join(process.cwd(), "messages", `${locale}.json`), "utf8")
		).notebook_research;
		for (const key of PAGE_KEYS) assert.ok(catalogue[key], `${locale} ${key}`);
	}
});
