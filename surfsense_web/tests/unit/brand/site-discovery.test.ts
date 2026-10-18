import assert from "node:assert/strict";
import test from "node:test";
import { organizationData } from "@/components/seo/json-ld";
import { type Brand, resolveBrand } from "@/lib/brand";
import brandJson from "@/lib/brand/brand.json";
import { robotsRules } from "@/lib/seo/robots";
import { PUBLIC_PAGES, sitemapEntries } from "@/lib/seo/sitemap";

// Run with: pnpm exec tsx --test tests/unit/brand/site-discovery.test.ts

const unconfigured = brandJson as Brand;
const configured = resolveBrand(unconfigured, {
	siteUrl: "https://notes.example.org",
	contactEmail: "help@example.org",
});
const when = new Date("2026-10-11T02:00:00Z");

test("the sitemap lists every public page and doc on the server's own address", () => {
	const entries = sitemapEntries(["/docs", "/docs/studio"], when, configured);
	const urls = entries.map((entry) => entry.url);
	assert.equal(urls[0], "https://notes.example.org/");
	assert.ok(urls.includes("https://notes.example.org/pricing"));
	assert.ok(urls.includes("https://notes.example.org/docs/studio"));
	assert.equal(urls.length, PUBLIC_PAGES.length + 1, "/docs is listed once");
	assert.ok(entries.every((entry) => entry.lastModified === when));
});

test("a server with no address publishes an empty sitemap and no Sitemap line", () => {
	assert.deepEqual(sitemapEntries(["/docs/studio"], when, unconfigured), []);
	assert.equal(robotsRules(unconfigured).sitemap, undefined);
	assert.equal(robotsRules(configured).sitemap, "https://notes.example.org/sitemap.xml");
	assert.ok(JSON.stringify(robotsRules(unconfigured).rules).includes("/dashboard/"));
});

test("the organisation names a contact point only when the server publishes one", () => {
	const data = organizationData(configured);
	assert.equal(data.url, "https://notes.example.org/");
	assert.equal(data.logo, "https://notes.example.org/logo.png");
	assert.deepEqual(data.contactPoint, {
		"@type": "ContactPoint",
		email: "help@example.org",
		contactType: "customer support",
	});
	const bare = JSON.parse(JSON.stringify(organizationData(unconfigured)));
	assert.equal(bare.name, "Soloclef Notes");
	assert.equal("url" in bare, false);
	assert.equal("logo" in bare, false);
	assert.equal("contactPoint" in bare, false);
});
