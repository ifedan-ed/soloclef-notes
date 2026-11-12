/**
 * The headers every page of the web app is served with (next.config.ts).
 *
 * Why: nothing else sets them. Neither Caddy in front nor Next.js adds them
 * by default, so until now any page could be framed by any site
 * (clickjacking: a framed "Delete" or "Make public" under a decoy button).
 * The session cookies are SameSite=Lax, which keeps a cross-site frame
 * signed out, but every other host under the same registrable domain counts
 * as the same site and would frame the app signed in.
 *
 * Only what cannot break a page is here: who may frame it, no MIME sniffing,
 * and how much of the address a link passes on. A script policy is not:
 * the app's inline scripts and its third parties would need a nonce plan of
 * their own. The app frames nothing of its own origin (its HTML viewers use
 * sandboxed srcDoc frames), so 'self' is enough and nothing is lost.
 *
 * Kept out of next.config.ts because that module cannot be imported from a
 * test (createMDX() spawns esbuild on load); tests/unit/security-headers.test.ts
 * checks this list.
 */
export const PAGE_SECURITY_HEADERS: ReadonlyArray<{ key: string; value: string }> = [
	// Both spellings: frame-ancestors is the standard, X-Frame-Options is what
	// older browsers and some in-app web views still read.
	{ key: "Content-Security-Policy", value: "frame-ancestors 'self'" },
	{ key: "X-Frame-Options", value: "SAMEORIGIN" },
	{ key: "X-Content-Type-Options", value: "nosniff" },
	// A share link's token is in the page's path: a link out of a shared page
	// must not hand the whole address to the site it opens.
	{ key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
];

/** next.config.ts `headers()`: the list on every path. */
export function pageSecurityHeaderRules() {
	return [{ source: "/:path*", headers: [...PAGE_SECURITY_HEADERS] }];
}
