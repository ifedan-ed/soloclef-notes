/**
 * Old docs URLs.
 *
 * The upstream docs were versioned (`/docs/legacy/...` for 0.0.40,
 * `/docs/v2` for the rebuild) and, before that, sat directly under `/docs`.
 * None of those pages describe this app, so they were deleted. Their URLs
 * send people to the docs front page instead of a 404.
 *
 * Kept out of `next.config.ts` because that module cannot be imported from a
 * test: `createMDX()` spawns esbuild on load. `tests/unit/docs-redirects.test.ts`
 * asserts these patterns against real path matching.
 */

/** Version folders the upstream docs used. */
export const RETIRED_VERSION_SEGMENTS = ["legacy", "v2"] as const;

/** Top-level sections of the upstream docs from before they were versioned. */
export const RETIRED_SECTIONS = [
	"installation",
	"manual-installation",
	"docker-installation",
	"how-to",
	"connectors",
	"messaging-channels",
	"local-models",
	"observability",
	"testing",
	"code-of-conduct",
] as const;

const retired = [...RETIRED_VERSION_SEGMENTS, ...RETIRED_SECTIONS].join("|");

/**
 * Pages of this app's own docs that were renamed: the old address goes to the
 * page that replaced it. The workspaces page became the account's settings
 * and sharing page when workspaces were removed.
 */
export const RENAMED_PAGES: Record<string, string> = {
	"workspaces-and-sharing": "account-and-sharing",
};

export const docsRedirects = [
	{ source: `/docs/:section(${retired})`, destination: "/docs", permanent: true },
	{ source: `/docs/:section(${retired})/:path*`, destination: "/docs", permanent: true },
	...Object.entries(RENAMED_PAGES).map(([from, to]) => ({
		source: `/docs/${from}`,
		destination: `/docs/${to}`,
		permanent: true,
	})),
];
