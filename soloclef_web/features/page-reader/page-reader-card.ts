import { z } from "zod";

/** The chat's page reader, as the model calls it (soloclef_api/internal/chat/pagereader). */
export const PAGE_READER_TOOL = "web_crawl";

/**
 * How one page fared. "empty" is a page that answered and had no readable
 * text (drawn by script, or blank); "failed" is one that could not be
 * fetched at all. They are told apart on the card because only the second
 * may be worth trying again.
 */
const pageStatus = z.enum(["success", "empty", "failed"]);
export type PageStatus = z.infer<typeof pageStatus>;

const page = z.object({
	url: z.string(),
	status: pageStatus,
	/** At most the first 500 characters of the page's text. */
	content_preview: z.string().optional(),
	/** Why the page has no text, in the reader's own sentence. */
	error: z.string().optional(),
});
export type PageReaderPage = z.infer<typeof page>;

/**
 * What the server sends for a finished page-reader call. These fields are
 * the whole payload: the server builds it from an allowlist, because a card
 * is saved with the message and published with a shared thread, and a
 * page's full text must never be in it.
 */
const card = z.object({
	status: z.literal("completed"),
	pages: z.array(page).min(1),
	succeeded: z.number().int().nonnegative(),
	total: z.number().int().positive(),
});
export type PageReaderCardData = z.infer<typeof card>;

/**
 * The card for a tool result, or null when the result is not one: a call
 * still running or waiting for approval, a call that failed as a whole
 * (the server then sends only a length), or a shape from another version.
 * Null leaves the call where every other process step is shown, in the
 * turn's trace.
 */
export function parsePageReaderCard(result: unknown): PageReaderCardData | null {
	const parsed = card.safeParse(result);
	return parsed.success ? parsed.data : null;
}

/** "Read 2 of 3 pages", "Read 1 page", "Could not read 2 pages". */
export function pageReaderSummary(data: PageReaderCardData): string {
	const { succeeded, total } = data;
	const pages = total === 1 ? "page" : "pages";
	if (succeeded === 0)
		return total === 1 ? "Could not read the page" : `Could not read ${total} pages`;
	if (succeeded === total) return `Read ${total} ${pages}`;
	return `Read ${succeeded} of ${total} pages`;
}

/**
 * An address split for display: the site, and the rest of it. `href` is set
 * only for http and https, so an address the model made up in some other
 * scheme is shown as text and is never a link someone can press.
 */
export function pageAddress(url: string): { host: string; rest: string; href: string | null } {
	try {
		const parsed = new URL(url);
		if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
			return { host: url, rest: "", href: null };
		}
		const rest = `${parsed.pathname}${parsed.search}`;
		return {
			host: parsed.hostname.replace(/^www\./, ""),
			rest: rest === "/" ? "" : rest,
			href: parsed.href,
		};
	} catch {
		return { host: url, rest: "", href: null };
	}
}
