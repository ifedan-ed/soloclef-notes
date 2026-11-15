import { createMDX } from "fumadocs-mdx/next";
import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

// Create the next-intl plugin
const withNextIntl = createNextIntlPlugin("./i18n/request.ts");

// The public pages are drawn at build time with this server's own address
// (lib/brand; the Dockerfile's build args). Without one they carry no
// absolute links and an empty sitemap, which is right for a server that has
// none yet but wrong for a live site built by hand, so the build log says so.
if (process.env.NODE_ENV === "production" && !process.env.NEXT_PUBLIC_SITE_URL?.trim()) {
	console.warn(
		"NEXT_PUBLIC_SITE_URL is empty: the sitemap, robots.txt, canonical and share-card links " +
			"will name no site. Build through docker compose (it passes SOLOCLEF_PUBLIC_URL) or set it."
	);
}

// `next build` writes the site as static files (out/), which the stack's
// Caddy serves (docker/proxy-go/routes.caddy); no Node process runs on the
// server. Everything a Next.js server used to do at request time has a new
// home, each named where it went:
//
//   - the security headers, the old-address redirects, the signed-in "/"
//     redirect, the address of a dynamic page (/dashboard/notebooks/<id>,
//     /public/<token>, ...) and the cache headers: Caddy;
//   - the /api/v1, /auth and /verify-token pass-throughs: Caddy already sends
//     those paths to the API directly;
//   - what a page read on the server: the cookies and the support address
//     are read in the browser, and the sign-in page's SSO button is the
//     build's setting, as the docs' wording already was;
//   - docs search: an index built here and searched in the browser.
//
// `next dev` is not an export: it serves every route itself and, given
// SOLOCLEF_BACKEND_INTERNAL_URL, passes the API's paths on to it, so a
// developer runs the app without Caddy.
const exporting = process.env.NODE_ENV === "production";
const devBackend = process.env.SOLOCLEF_BACKEND_INTERNAL_URL?.trim().replace(/\/+$/, "");

const nextConfig: NextConfig = {
	...(exporting ? { output: "export" as const } : {}),
	...(!exporting && devBackend
		? {
				async rewrites() {
					// The same paths routes.caddy sends to the API.
					return ["/api/v1/:path*", "/auth/:path*", "/users/:path*", "/verify-token"].map(
						(source) => ({ source, destination: `${devBackend}${source}` })
					);
				},
			}
		: {}),
	reactStrictMode: false,
	typescript: {
		ignoreBuildErrors: true,
	},
	images: {
		// There is no image server any more: an image is fetched by the
		// browser as it is. The app's own pictures are already sized for
		// where they appear, and a remote picture in an answer was never worth
		// a round trip through the server.
		unoptimized: true,
	},
	experimental: {
		optimizePackageImports: [
			"lucide-react",
			"@tabler/icons-react",
			"date-fns",
			"@assistant-ui/react",
			"@assistant-ui/react-markdown",
			"motion",
		],
	},
	// Turbopack config (used during `next dev --turbopack`)
	turbopack: {
		rules: {
			"*.svg": {
				loaders: ["@svgr/webpack"],
				as: "*.js",
			},
		},
	},

	// Configure webpack (SVGR)
	webpack: (config) => {
		// SVGR: import *.svg as React components
		const fileLoaderRule = config.module.rules.find(
			(rule: { test?: { test?: (s: string) => boolean } }) => rule.test?.test?.(".svg")
		);
		config.module.rules.push(
			// Re-apply the existing file loader for *.svg?url imports
			{
				...fileLoaderRule,
				test: /\.svg$/i,
				resourceQuery: /url/, // e.g. import icon from './icon.svg?url'
			},
			// Convert all other *.svg imports to React components
			{
				test: /\.svg$/i,
				issuer: fileLoaderRule.issuer,
				resourceQuery: { not: [...fileLoaderRule.resourceQuery.not, /url/] },
				use: ["@svgr/webpack"],
			}
		);
		fileLoaderRule.exclude = /\.svg$/i;

		return config;
	},
};

// Wrap the config with MDX and next-intl plugins
const withMDX = createMDX({});

export default withNextIntl(withMDX(nextConfig));
