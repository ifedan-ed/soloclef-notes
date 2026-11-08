/** A cheap shape check before asking the server; the server has the last word. */
export function looksLikeEmail(value: string): boolean {
	return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value.trim());
}
