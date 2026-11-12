import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import {
	ACADEMIC_SOURCE_IDS,
	READ_PAGE_ID,
	RESEARCH_SOURCE_IDS,
	WEB_API_SOURCE_IDS,
	WEB_GROUP_MENU_IDS,
} from "@/features/notebooks/model/research-sources";

const LOCALES = ["en", "es", "hi", "ko", "pt", "zh"] as const;

function research(locale: string): Record<string, string> {
	return JSON.parse(readFileSync(join(process.cwd(), "messages", `${locale}.json`), "utf8"))
		.notebook_research;
}

test("research strings exist in all six locales", () => {
	const english = Object.keys(research("en")).sort();
	for (const locale of LOCALES) {
		assert.deepEqual(Object.keys(research(locale)).sort(), english, locale);
	}
});

test("every source the box offers has a name in every locale", () => {
	// The menu names each choice through `source_<id>`, so a missing one would
	// show its dotted key to people.
	for (const locale of LOCALES) {
		const catalogue = research(locale);
		for (const id of [...RESEARCH_SOURCE_IDS, READ_PAGE_ID]) {
			assert.ok(catalogue[`source_${id}`], `${locale} ${id}`);
		}
	}
});

test("Exa and Tavily are named, and their unavailable and error wording exists everywhere", () => {
	for (const locale of LOCALES) {
		const catalogue = research(locale);
		for (const id of WEB_API_SOURCE_IDS) assert.ok(catalogue[`source_${id}`], `${locale} ${id}`);
		for (const key of [
			"unavailable_not_connected",
			"unavailable_not_available",
			"error_source_unavailable",
			"unavailable_hint",
			"error_source_not_connected",
			"error_source_key_rejected",
			"error_source_quota",
		]) {
			assert.ok(catalogue[key], `${locale} ${key}`);
		}
		for (const code of ["not_connected", "key_rejected", "quota"]) {
			assert.match(catalogue[`error_source_${code}`], /\{source\}/, `${locale} ${code}`);
		}
	}
});

test("the menu's two groups offer every searchable source exactly once", () => {
	const offered = [...WEB_GROUP_MENU_IDS, ...ACADEMIC_SOURCE_IDS].filter(
		(id) => id !== READ_PAGE_ID
	);
	assert.deepEqual([...offered].sort(), [...RESEARCH_SOURCE_IDS].sort());
});
