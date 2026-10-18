/** The URL a person opens, on whatever origin they are using the app from. */
export function publicLinkUrl(origin: string, shareToken: string): string {
	return `${origin.replace(/\/$/, "")}/public/${shareToken}`;
}

/** Where a shared output's file is served, relative to the backend. */
export function publicFilePath(shareToken: string, artifactId: number): string {
	return `/api/v1/public/${shareToken}/artifacts/${artifactId}/content`;
}

/** The address of a notebook's live link. */
export function liveNotebookUrl(origin: string, shareToken: string): string {
	return `${origin.replace(/\/$/, "")}/public/notebook/${shareToken}`;
}

/** A live notebook link's public API, relative to the backend. */
export function liveNotebookApiPath(shareToken: string, rest = ""): string {
	return `/api/v1/public-notebooks/${encodeURIComponent(shareToken)}${rest}`;
}
