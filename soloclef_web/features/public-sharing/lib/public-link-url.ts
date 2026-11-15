/** The URL a person opens, on whatever origin they are using the app from. */
export function publicLinkUrl(origin: string, shareToken: string): string {
	return `${origin.replace(/\/$/, "")}/public/${shareToken}`;
}

/** Where a shared output's file is served, relative to the backend. */
export function publicFilePath(shareToken: string, artifactId: number): string {
	return `/api/v1/public/${shareToken}/artifacts/${artifactId}/content`;
}
