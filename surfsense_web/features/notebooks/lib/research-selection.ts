import {
	MAX_RESEARCH_IMPORT,
	type ResearchImportItem,
	type ResearchResult,
} from "../model/research";
import type { SelectAllState } from "./source-selection";

/** Fresh results arrive checked, up to what one import can carry. */
export function initialSelection(results: ResearchResult[]): Set<string> {
	return new Set(results.slice(0, MAX_RESEARCH_IMPORT).map((result) => result.id));
}

export function toggleResult(selected: ReadonlySet<string>, id: string): Set<string> {
	const next = new Set(selected);
	if (next.has(id)) next.delete(id);
	else next.add(id);
	return next;
}

export function resultsSelectState(
	results: ResearchResult[],
	selected: ReadonlySet<string>
): SelectAllState {
	const count = results.filter((result) => selected.has(result.id)).length;
	if (count === 0) return "none";
	return count === results.length ? "all" : "some";
}

/** "Select all" checks everything unless everything is already checked. */
export function selectAllResults(results: ResearchResult[], state: SelectAllState): Set<string> {
	return state === "all" ? new Set() : new Set(results.map((result) => result.id));
}

/**
 * The import body for the checked results, in the order they were shown.
 * A URL listed twice is imported once.
 */
export function importItemsFor(
	results: ResearchResult[],
	selected: ReadonlySet<string>
): ResearchImportItem[] {
	const seen = new Set<string>();
	const items: ResearchImportItem[] = [];
	for (const result of results) {
		if (!selected.has(result.id) || seen.has(result.url)) continue;
		seen.add(result.url);
		items.push({
			url: result.url,
			title: result.title,
			snippet: result.snippet,
			source: result.source,
			...(result.paper ? { paper: result.paper } : {}),
		});
	}
	return items;
}

export function canImport(items: ResearchImportItem[]): boolean {
	return items.length > 0 && items.length <= MAX_RESEARCH_IMPORT;
}
