import assert from "node:assert/strict";
import test from "node:test";
import { OAUTH_RESULT_COOKIE, parseOAuthCallbackResult } from "@/contracts/types/oauth.types";
import { oauthResultCookie, oauthResultFromQuery } from "@/lib/connectors/oauth-callback";

// Run with: pnpm exec tsx --test tests/unit/static-site/client-reads.test.ts

// What pages now read in the browser that a Next.js server used to.

test("the OAuth callback's result reaches the connectors dialog in its cookie", () => {
	const result = oauthResultFromQuery("?success=true&connector=google-drive&connectorId=12");
	assert.deepEqual(result, {
		success: "true",
		error: null,
		connector: "google-drive",
		connectorId: "12",
	});
	const cookie = oauthResultCookie(result);
	assert.match(cookie, new RegExp(`^${OAUTH_RESULT_COOKIE}=`));
	assert.match(cookie, /; path=\/; max-age=60; samesite=lax$/);
	// The dialog's reader: decodeURIComponent of the value, then the schema.
	const value = cookie.slice(OAUTH_RESULT_COOKIE.length + 1).split(";")[0];
	assert.deepEqual(parseOAuthCallbackResult(decodeURIComponent(value)), result);
	// An error with characters a cookie cannot hold raw still round-trips.
	const odd = oauthResultFromQuery("?error=a%3Bb%3Dc%20d");
	const oddValue = oauthResultCookie(odd).split(";")[0].split("=").slice(1).join("=");
	assert.equal(parseOAuthCallbackResult(decodeURIComponent(oddValue))?.error, "a;b=c d");
});

test("a one-time marker is dropped from the address without a navigation", async () => {
	const { replaceAddressQuery } = await import("@/lib/routing/replace-query");
	const calls: string[] = [];
	const g = globalThis as { window?: unknown };
	const before = g.window;
	try {
		g.window = {
			location: { pathname: "/dashboard/settings/connectors", hash: "#x" },
			history: { replaceState: (_s: unknown, _t: string, url: string) => calls.push(url) },
		};
		replaceAddressQuery("");
		replaceAddressQuery("a=1");
		replaceAddressQuery("?b=2");
	} finally {
		g.window = before;
	}
	assert.deepEqual(calls, [
		"/dashboard/settings/connectors#x",
		"/dashboard/settings/connectors?a=1#x",
		"/dashboard/settings/connectors?b=2#x",
	]);
});
