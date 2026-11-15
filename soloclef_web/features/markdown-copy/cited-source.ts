// One entry of the "Sources" list a copy or download ends with.

/** A numbered source as a reader of a pasted answer needs it. */
export interface CitedSource {
	number: number;
	/** The document's title (with its page when known), or the page's address. */
	name: string;
	/** Where it can be opened from outside the app: only a web address has one. */
	href?: string;
}

/**
 * Said for a cited passage whose document could not be named: deleted since,
 * or the lookup failed. The number stays, so the text and the list agree.
 */
export const UNNAMED_SOURCE = "Source title unavailable";

/** The name of a cited document, with the page the passage is on when known. */
export function documentSourceName(title: string, page: number | null | undefined): string {
	return page ? `${title}, p. ${page}` : title;
}
