/**
 * The token from a reset link. The email puts it in the fragment
 * (`/reset-password#token=...`) so it never reaches a server log; a `?token=`
 * link is accepted too.
 */
export function readResetToken(hash: string, search: string): string | null {
	const fromHash = new URLSearchParams(hash.replace(/^#/, "")).get("token");
	const fromQuery = new URLSearchParams(search.replace(/^\?/, "")).get("token");
	const token = (fromHash || fromQuery || "").trim();
	return token.length > 0 ? token : null;
}
