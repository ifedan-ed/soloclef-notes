/** Every scraper capability a run can have, for the runs filter. */
export const RUN_CAPABILITIES = [
	"reddit.scrape",
	"youtube.scrape",
	"youtube.comments",
	"instagram.scrape",
	"instagram.details",
	"tiktok.scrape",
	"tiktok.comments",
	"tiktok.user_search",
	"tiktok.trending",
	"google_maps.scrape",
	"google_maps.reviews",
	"google_search.scrape",
	"indeed.scrape",
	"amazon.scrape",
	"walmart.scrape",
	"walmart.reviews",
	"web.crawl",
	"exa.web_search",
	"tavily.web_search",
	"browserbase.read_page",
] as const;

/** Notebook research searches, recorded as ``research.<source>``. */
export const RESEARCH_RUN_CAPABILITIES = [
	"research.web",
	"research.google_search",
	"research.exa",
	"research.tavily",
	"research.youtube",
	"research.reddit",
	"research.instagram",
	"research.tiktok",
	"research.google_maps",
	"research.indeed",
	"research.amazon",
	"research.walmart",
	"research.pubmed",
	"research.arxiv",
	"research.medrxiv",
	"research.google_scholar",
] as const;

/** Where a run was started from; anything else is shown as recorded. */
export const RUN_ORIGINS = ["ui", "api", "agent", "notebook"] as const;
export type RunOrigin = (typeof RUN_ORIGINS)[number];

export function isRunOrigin(value: string): value is RunOrigin {
	return (RUN_ORIGINS as readonly string[]).includes(value);
}

export const RUN_STATUSES = ["running", "success", "error", "cancelled"] as const;
export type RunStatus = (typeof RUN_STATUSES)[number];

export function isRunStatus(value: string): value is RunStatus {
	return (RUN_STATUSES as readonly string[]).includes(value);
}
