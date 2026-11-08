import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import {
	ACADEMIC_SOURCE_IDS,
	MAY_RETURN_NOTHING,
	RESEARCH_SOURCE_IDS,
	WEB_API_SOURCE_IDS,
	WEB_GROUP_SOURCE_IDS,
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

test("every academic source has a name and every caveated source its explanation", () => {
	for (const locale of LOCALES) {
		const catalogue = research(locale);
		for (const id of ACADEMIC_SOURCE_IDS) assert.ok(catalogue[`source_${id}`], `${locale} ${id}`);
		for (const id of MAY_RETURN_NOTHING) {
			assert.ok(catalogue[`caveat_${id}`], `${locale} caveat ${id}`);
			assert.ok(catalogue[`no_results_${id}`], `${locale} no_results ${id}`);
		}
	}
});

test("Exa and Tavily are named, and their unavailable and error wording exists everywhere", () => {
	for (const locale of LOCALES) {
		const catalogue = research(locale);
		for (const id of WEB_API_SOURCE_IDS) assert.ok(catalogue[`source_${id}`], `${locale} ${id}`);
		for (const key of [
			"unavailable_not_connected",
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

test("the web group lists the general searches first, then Exa and Tavily, then the platforms", () => {
	assert.deepEqual(WEB_GROUP_SOURCE_IDS.slice(0, 4), ["web", "google_search", "exa", "tavily"]);
	const academic = new Set<string>(ACADEMIC_SOURCE_IDS);
	const everyNonAcademic = RESEARCH_SOURCE_IDS.filter((id) => !academic.has(id));
	assert.deepEqual([...WEB_GROUP_SOURCE_IDS].sort(), [...everyNonAcademic].sort());
});
