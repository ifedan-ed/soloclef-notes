import assert from "node:assert/strict";
import test from "node:test";

// Run with: pnpm exec tsx --test tests/unit/security-headers.test.ts

// The pages' own headers, as next.config.ts serves them. Without them any
// site could frame the app (clickjacking: a framed "Delete" or "Make public"
// under a decoy button). SameSite=Lax keeps cross-site frames signed out, but
// every host under the same registrable domain is the same site, so the
// cookies are sent there.
import { PAGE_SECURITY_HEADERS, pageSecurityHeaderRules } from "../../lib/security-headers";

function header(name: string): string | undefined {
	return PAGE_SECURITY_HEADERS.find((entry) => entry.key.toLowerCase() === name.toLowerCase())
		?.value;
}

test("no other site may frame a page", () => {
	assert.equal(header("X-Frame-Options"), "SAMEORIGIN");
	assert.match(header("Content-Security-Policy") ?? "", /frame-ancestors 'self'/);
});

test("pages are not sniffed and do not leak their address", () => {
	assert.equal(header("X-Content-Type-Options"), "nosniff");
	assert.equal(header("Referrer-Policy"), "strict-origin-when-cross-origin");
});

test("the rule covers every page", () => {
	const rules = pageSecurityHeaderRules();
	assert.equal(rules.length, 1);
	assert.equal(rules[0].source, "/:path*");
	assert.deepEqual(rules[0].headers, PAGE_SECURITY_HEADERS);
});
