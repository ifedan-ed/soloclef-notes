import type { ResearchMode, ResearchRequest } from "../model/research";
import {
	isResearchBoxSource,
	isResearchSourceId,
	READ_PAGE_ID,
	type ResearchBoxSource,
	type ResearchSourceId,
	type UnavailableSources,
} from "../model/research-sources";

/** One search can ask this many sources at once (the backend refuses more). */
export const MAX_RESEARCH_SOURCES = 4;

/**
 * Where the research box looks. By default one source, picked with one click;
 * with ``several`` on, up to ``MAX_RESEARCH_SOURCES`` ticked sources searched
 * side by side. ``source`` is always the first of ``sources`` unless it is
 * "Read a web page", which reads one link and is never searched with others.
 */
export interface SourcePicks {
	source: ResearchBoxSource;
	several: boolean;
	sources: ResearchSourceId[];
}

export const initialSourcePicks: SourcePicks = { source: "web", several: false, sources: ["web"] };

/** One click on a source: search only it (or read a page), and leave "several" mode. */
export function pickOnly(picks: SourcePicks, id: ResearchBoxSource): SourcePicks {
	if (id === READ_PAGE_ID) return { ...picks, source: id, several: false };
	return { source: id, several: false, sources: [id] };
}

/** Turn "search several at once" on (starting from the current source) or off (keeping the first). */
export function setSeveral(picks: SourcePicks, several: boolean): SourcePicks {
	const first: ResearchSourceId = isResearchSourceId(picks.source)
		? picks.source
		: (picks.sources[0] ?? "web");
	if (!several) return { source: first, several: false, sources: [first] };
	const sources = picks.sources.includes(first)
		? [first, ...picks.sources.filter((id) => id !== first)]
		: [first];
	return { source: first, several: true, sources: sources.slice(0, MAX_RESEARCH_SOURCES) };
}

/** Whether ``id`` can still be ticked: it is ticked already, or there is room. */
export function canTick(picks: SourcePicks, id: ResearchSourceId): boolean {
	return picks.sources.includes(id) || picks.sources.length < MAX_RESEARCH_SOURCES;
}

/** Tick or untick one source; the last one stays ticked and the cap holds. */
export function toggleSource(picks: SourcePicks, id: ResearchSourceId): SourcePicks {
	const ticked = picks.sources.includes(id);
	if (ticked && picks.sources.length === 1) return picks;
	if (!ticked && !canTick(picks, id)) return picks;
	const sources = ticked ? picks.sources.filter((s) => s !== id) : [...picks.sources, id];
	return { source: sources[0], several: true, sources };
}

/** The sources a search with these picks asks; empty when reading a page. */
export function searchedSources(picks: SourcePicks): ResearchSourceId[] {
	if (picks.source === READ_PAGE_ID) return [];
	return picks.several ? picks.sources : [picks.source];
}

/**
 * The picks with sources this workspace cannot search taken out (an Exa key
 * removed since the picks were remembered). Nothing left falls back to Web.
 */
export function withoutUnavailable(
	picks: SourcePicks,
	unavailable: UnavailableSources
): SourcePicks {
	const sources = picks.sources.filter((id) => !unavailable[id]);
	if (sources.length === picks.sources.length) return picks;
	const kept = sources.length > 0 ? sources : (["web"] as ResearchSourceId[]);
	const source = picks.source === READ_PAGE_ID ? picks.source : kept[0];
	return { source, several: picks.several, sources: kept };
}

/** Remembered picks read back, or null when they are missing or no longer make sense. */
export function parseSourcePicks(value: unknown): SourcePicks | null {
	if (!value || typeof value !== "object") return null;
	const raw = value as Record<string, unknown>;
	if (!isResearchBoxSource(raw.source) || !Array.isArray(raw.sources)) return null;
	const sources = [...new Set(raw.sources.filter(isResearchSourceId))].slice(
		0,
		MAX_RESEARCH_SOURCES
	);
	if (sources.length === 0) return null;
	const several = raw.several === true;
	if (raw.source === READ_PAGE_ID) return { source: READ_PAGE_ID, several: false, sources };
	return several
		? { source: sources[0], several: true, sources }
		: { source: raw.source, several: false, sources: [raw.source] };
}

/** The search request for a query with these picks: ``sources`` only when there are several. */
export function researchRequestFor(
	query: string,
	picks: SourcePicks,
	mode: ResearchMode
): ResearchRequest | null {
	const [first, ...rest] = searchedSources(picks);
	if (!first) return null;
	return rest.length > 0
		? { query, source: first, sources: [first, ...rest], mode }
		: { query, source: first, mode };
}
