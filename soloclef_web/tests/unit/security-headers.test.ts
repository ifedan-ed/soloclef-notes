import assert from "node:assert/strict";
import test from "node:test";
import { caddyLines } from "./caddy-routes";

// Run with: pnpm exec tsx --test tests/unit/security-headers.test.ts

// The pages' own headers, as the stack's Caddy serves them with the static
// site (docker/proxy-go/routes.caddy). Without them any site could frame the
// app (clickjacking: a framed "Delete" or "Make public" under a decoy
// button). SameSite=Lax keeps cross-site frames signed out, but every host
// under the same registrable domain is the same site, so the cookies are
// sent there.

/** The `header { ... }` block with no matcher: the one every page gets. */
function pageHeaders(): Map<string, string> {
	const lines = caddyLines();
	const start = lines.indexOf("header {");
	assert.ok(start >= 0, "routes.caddy has a header block for the pages");
	const headers = new Map<string, string>();
	for (const line of lines.slice(start + 1)) {
		if (line === "}") break;
		const match = /^(\S+)\s+"(.*)"$/.exec(line);
		assert.ok(match, `unreadable header line: ${line}`);
		headers.set(match[1].toLowerCase(), match[2]);
	}
	return headers;
}

test("no other site may frame a page", () => {
	assert.equal(pageHeaders().get("x-frame-options"), "SAMEORIGIN");
	assert.match(pageHeaders().get("content-security-policy") ?? "", /frame-ancestors 'self'/);
});

test("pages are not sniffed and do not leak their address", () => {
	assert.equal(pageHeaders().get("x-content-type-options"), "nosniff");
	assert.equal(pageHeaders().get("referrer-policy"), "strict-origin-when-cross-origin");
});

test("the headers are set for the pages, after every API path is passed on", () => {
	// Inside the one route, in order: everything before the block is the
	// API's (it answers with its own headers), everything after it a page.
	const lines = caddyLines();
	const block = lines.indexOf("header {");
	const lastProxy = lines.map((line) => line.startsWith("reverse_proxy ")).lastIndexOf(true);
	assert.ok(lastProxy >= 0 && lastProxy < block, "every reverse_proxy comes before the headers");
	const fileServer = lines.findIndex((line) => line.startsWith("file_server"));
	assert.ok(fileServer > block, "the pages are served after the headers");
});
