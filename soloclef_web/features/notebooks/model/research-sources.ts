/** Scholarly sources, searched through their own APIs. */
export const ACADEMIC_SOURCE_IDS = ["pubmed", "arxiv", "medrxiv", "google_scholar"] as const;
export type AcademicSourceId = (typeof ACADEMIC_SOURCE_IDS)[number];

/** Web search APIs answered with the account's own key (its Exa / Tavily connector). */
export const WEB_API_SOURCE_IDS = ["exa", "tavily"] as const;
export type WebApiSourceId = (typeof WEB_API_SOURCE_IDS)[number];

/**
 * Everything the "Search the web for new sources" box can search. The
 * platform scrapers it used to list (the open web, Google Search, YouTube,
 * Reddit and the rest) were cut from the product, so the web side is the
 * account's own keyed search APIs only.
 */
export const RESEARCH_SOURCE_IDS = [...WEB_API_SOURCE_IDS, ...ACADEMIC_SOURCE_IDS] as const;
export type ResearchSourceId = (typeof RESEARCH_SOURCE_IDS)[number];

export function isResearchSourceId(value: unknown): value is ResearchSourceId {
	return typeof value === "string" && (RESEARCH_SOURCE_IDS as readonly string[]).includes(value);
}

/** Why a source cannot be searched: no key connected, or not searched by this server. */
export type SourceUnavailableReason = "not_connected" | "not_available";
export type UnavailableSources = Partial<Record<ResearchSourceId, SourceUnavailableReason>>;

export function isWebApiSourceId(value: unknown): value is WebApiSourceId {
	return typeof value === "string" && (WEB_API_SOURCE_IDS as readonly string[]).includes(value);
}

export function isAcademicSourceId(value: unknown): value is AcademicSourceId {
	return typeof value === "string" && (ACADEMIC_SOURCE_IDS as readonly string[]).includes(value);
}

/**
 * Not a search: "Read a web page" adds one pasted URL as a source. Browserbase
 * opens it in a real browser when the account has it connected, so pages
 * built with JavaScript work; otherwise the ordinary crawler reads it.
 */
export const READ_PAGE_ID = "page" as const;
/** What the research box can do: search a source, or read one page. */
export type ResearchBoxSource = ResearchSourceId | typeof READ_PAGE_ID;

export function isResearchBoxSource(value: unknown): value is ResearchBoxSource {
	return value === READ_PAGE_ID || isResearchSourceId(value);
}

/** The web group of the menu: "Read a web page" sits right after Exa and Tavily. */
export const WEB_GROUP_MENU_IDS: readonly ResearchBoxSource[] = [
	...WEB_API_SOURCE_IDS,
	READ_PAGE_ID,
];

/** What reads a pasted page for this account. */
export type PageReader = "browserbase" | "crawler";

/** A pasted page URL the backend will accept the shape of (it checks the host). */
export function isPageUrl(value: string): boolean {
	const text = value.trim();
	if (text.length === 0 || text.length > 2048) return false;
	try {
		const url = new URL(text);
		return (url.protocol === "http:" || url.protocol === "https:") && url.hostname.length > 0;
	} catch {
		return false;
	}
}
