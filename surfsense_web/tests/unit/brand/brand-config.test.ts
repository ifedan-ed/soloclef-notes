import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";
import { BRAND, type Brand, brandTitle, metadataBase, resolveBrand, siteUrl } from "@/lib/brand";
import brandJson from "@/lib/brand/brand.json";
import markJson from "@/lib/brand/mark.json";
import { BRAND_TOKEN, withBrand } from "@/lib/brand/messages";
import { buildReportMailto } from "@/lib/brand/report";
import { signedInHomeRedirect } from "@/lib/home-redirect";

const WEB = path.resolve(__dirname, "../../..");
const base = brandJson as Brand;

test("brand.json names the product and has every field the site reads", () => {
	assert.equal(base.name, "Soloclef Notes");
	for (const key of ["name", "shortName", "tagline", "description"] as const) {
		assert.ok(base[key].trim().length > 0, `${key} is empty`);
	}
	for (const color of Object.values(base.colors)) assert.match(color, /^#[0-9a-f]{6}$/i);
	for (const logo of Object.values(base.logo)) {
		assert.ok(logo.startsWith("/"), `${logo} is not a site path`);
		readFileSync(path.join(WEB, "public", logo));
	}
	assert.doesNotMatch(JSON.stringify(base), /surfsense/i);
});

test("brand.json carries no server's address or contact: each build sets its own", () => {
	assert.equal(base.siteUrl, "");
	assert.equal(base.contactEmail, "");
	assert.doesNotMatch(JSON.stringify(base), /example|soloclef\.com|@/i);
	assert.equal(BRAND.siteUrl, process.env.NEXT_PUBLIC_SITE_URL?.trim().replace(/\/+$/, "") || "");
	assert.equal(BRAND.contactEmail, process.env.NEXT_PUBLIC_SUPPORT_EMAIL?.trim() || "");
});

test("BRAND is brand.json unless the build overrides it", () => {
	assert.equal(BRAND.name, process.env.NEXT_PUBLIC_BRAND_NAME?.trim() || base.name);
	assert.equal(resolveBrand(base).name, base.name);
	assert.equal(resolveBrand(base, { name: "  " }).name, base.name, "blank override is ignored");
	const other = resolveBrand(base, { name: "Other Notes", siteUrl: "https://example.org///" });
	assert.equal(other.name, "Other Notes");
	assert.equal(other.siteUrl, "https://example.org");
	assert.equal(other.tagline, base.tagline, "the rest is kept");
	const contact = resolveBrand(base, { contactEmail: " help@example.org " });
	assert.equal(contact.contactEmail, "help@example.org");
	assert.equal(resolveBrand(base, { contactEmail: "  " }).contactEmail, "");
});

test("brandTitle and siteUrl", () => {
	assert.equal(brandTitle("Pricing", base), "Pricing · Soloclef Notes");
	assert.equal(brandTitle(undefined, base), "Soloclef Notes");
	assert.equal(brandTitle("   ", base), "Soloclef Notes");
	const site = resolveBrand(base, { siteUrl: "https://notes.example.org/" });
	assert.equal(siteUrl("/", site), "https://notes.example.org/");
	assert.equal(siteUrl("pricing", site), "https://notes.example.org/pricing");
	assert.equal(siteUrl("/docs/studio", site), "https://notes.example.org/docs/studio");
	assert.equal(metadataBase(site)?.href, "https://notes.example.org/");
});

test("a server with no address prints no absolute links at all", () => {
	assert.equal(siteUrl("/", base), undefined);
	assert.equal(siteUrl("/docs/studio", base), undefined);
	assert.equal(metadataBase(base), undefined);
});

test("the mark geometry is complete", () => {
	assert.equal(markJson.viewBox, 64);
	assert.ok(markJson.lines.length >= 1);
	for (const [x1, y, x2] of markJson.lines) {
		assert.ok(x1 < x2 && x2 <= 64 && y > 0 && y < 64);
	}
	assert.equal(markJson.dot.length, 3);
});

test("withBrand fills {brand} everywhere, deeply, and leaves ICU arguments alone", () => {
	const messages = {
		a: "Welcome to {brand}",
		nested: { b: "{brand} and {brand}", c: "Hello {name}", list: ["x {brand}"] },
		n: 3,
	};
	const filled = withBrand(messages, "Test Notes");
	assert.deepEqual(filled, {
		a: "Welcome to Test Notes",
		nested: { b: "Test Notes and Test Notes", c: "Hello {name}", list: ["x Test Notes"] },
		n: 3,
	});
	assert.equal(messages.a, "Welcome to {brand}", "the source object is not mutated");
	assert.equal(withBrand(messages), withBrand(messages), "the default name is cached per object");
});

test("locale files spell the product name only as {brand}", () => {
	const dir = path.join(WEB, "messages");
	const files = readdirSync(dir).filter((f) => f.endsWith(".json"));
	assert.equal(files.length, 6);
	for (const file of files) {
		const text = readFileSync(path.join(dir, file), "utf8");
		assert.doesNotMatch(text, /SurfSense/, `${file} still says SurfSense`);
		assert.doesNotMatch(text, new RegExp(base.name), `${file} hardcodes the brand name`);
		const filled = JSON.stringify(withBrand(JSON.parse(text)));
		assert.doesNotMatch(filled, BRAND_TOKEN, `${file} has an unfilled {brand}`);
		const auth = JSON.parse(text).auth;
		for (const key of ["request_access", "signups_closed_title", "signups_closed_body"]) {
			assert.ok(auth[key]?.trim(), `${file} is missing auth.${key}`);
		}
	}
});

test("problem reports go to the operator by email", () => {
	const operator = resolveBrand(base, { contactEmail: "help@example.org" });
	const url = buildReportMailto(["## Bug Report", "- **Page:** `/dashboard`"], operator);
	assert.ok(url.startsWith("mailto:help@example.org?"));
	assert.doesNotMatch(url, /\+/, "spaces are %20, not +");
	const params = new URLSearchParams(url.slice(url.indexOf("?") + 1));
	assert.equal(params.get("subject"), "Soloclef Notes: problem report");
	assert.equal(params.get("body"), "## Bug Report\n- **Page:** `/dashboard`");
	// No published address: the report is still written, the recipient left blank.
	assert.match(buildReportMailto(["x"], base, ""), /^mailto:\?subject=Soloclef%20Notes/);
});

test("/ sends a visitor with a session cookie to the dashboard, and nobody else", () => {
	const has = (names: string[]) => (name: string) => names.includes(name);
	assert.equal(signedInHomeRedirect("/", has(["surfsense_session"])), "/dashboard");
	assert.equal(signedInHomeRedirect("/", has(["surfsense_refresh"])), "/dashboard");
	assert.equal(signedInHomeRedirect("/", has([])), null);
	assert.equal(signedInHomeRedirect("/", has(["other"])), null);
	assert.equal(signedInHomeRedirect("/pricing", has(["surfsense_session"])), null);
});
