import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

// Run with: pnpm exec tsx --test tests/unit/docs-redirects.test.ts

// The copy Next matches routes with, so these assertions cannot drift from
// runtime behaviour. There is no standalone `path-to-regexp` in the tree.
import { compile, pathToRegexp } from "next/dist/compiled/path-to-regexp";
import brand from "../../lib/brand/brand.json";
import {
	docsRedirects,
	RETIRED_SECTIONS,
	RETIRED_VERSION_SEGMENTS,
} from "../../lib/docs-redirects";
import { DEFAULT_CATALOG } from "../../lib/pricing/catalog";

const matchers = docsRedirects.map((rule) => ({
	regexp: pathToRegexp(rule.source, []),
	toDestination: compile(rule.destination, { validate: false }),
}));

/** Applies the rules the way Next does: first match wins, or no redirect. */
function redirect(pathname: string): string | null {
	for (const { regexp, toDestination } of matchers) {
		if (regexp.exec(pathname)) return toDestination({});
	}
	return null;
}

const WEB_ROOT = path.join(__dirname, "../..");
const DOCS_CONTENT = path.join(WEB_ROOT, "content/docs");
const DOCS_PUBLIC = path.join(WEB_ROOT, "public/docs");

/** Every file below `dir`, as paths relative to it. */
function walk(dir: string, base = dir): string[] {
	return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
		const full = path.join(dir, entry.name);
		return entry.isDirectory() ? walk(full, base) : [path.relative(base, full)];
	});
}

const pages = walk(DOCS_CONTENT).filter((file) => file.endsWith(".mdx"));
const pageText = (file: string) => readFileSync(path.join(DOCS_CONTENT, file), "utf8");
const pageUrl = (file: string) =>
	`/docs/${file.replaceAll(path.sep, "/").replace(/(\/?index)?\.mdx$/, "")}`.replace(/\/$/, "");

test("the upstream docs' version folders go to the docs front page", () => {
	for (const segment of RETIRED_VERSION_SEGMENTS) {
		assert.equal(redirect(`/docs/${segment}`), "/docs", segment);
		assert.equal(redirect(`/docs/${segment}/installation`), "/docs", segment);
		assert.equal(redirect(`/docs/${segment}/connectors/native/reddit`), "/docs", segment);
	}
});

test("pre-versioning upstream URLs go to the docs front page", () => {
	for (const section of RETIRED_SECTIONS) {
		assert.equal(redirect(`/docs/${section}`), "/docs", section);
	}
	assert.equal(redirect("/docs/connectors/external/github"), "/docs");
	assert.equal(redirect("/docs/docker-installation/updating"), "/docs");
});

test("current pages and the front door are not redirected", () => {
	assert.equal(redirect("/docs"), null);
	assert.ok(pages.length >= 10, `expected the docs set, got ${pages.length}`);
	for (const file of pages) {
		assert.equal(redirect(pageUrl(file)), null, `${pageUrl(file)} would be redirected`);
	}
});

test("screenshots under public/docs exist, are referenced and are not redirected", () => {
	const assets = walk(DOCS_PUBLIC).map((file) => `/docs/${file.replaceAll(path.sep, "/")}`);
	assert.ok(assets.length > 0, "expected screenshots under public/docs");
	const allText = pages.map(pageText).join("\n");
	for (const asset of assets) {
		assert.equal(redirect(asset), null, `${asset} would 404`);
		assert.ok(allText.includes(asset), `${asset} is not used by any page`);
	}
	for (const [, ref] of allText.matchAll(/\]\((\/docs\/[^)\s]+\.(?:png|jpe?g|webp|gif))\)/g)) {
		assert.ok(existsSync(path.join(WEB_ROOT, "public", ref)), `${ref} is missing`);
	}
});

function readMeta(file: string): Record<string, unknown> {
	return JSON.parse(readFileSync(path.join(DOCS_CONTENT, file), "utf8"));
}

test("meta.json matches the schema fumadocs enforces and lists every page", async () => {
	// A wrong type here surfaces as `Module not found: .../meta.json.json`
	// from Turbopack, which names neither the field nor the reason.
	const { metaSchema } = await import("fumadocs-core/source/schema");
	const metas = walk(DOCS_CONTENT).filter((file) => path.basename(file) === "meta.json");
	assert.deepEqual(metas, ["meta.json"], "the docs are one flat section");
	const meta = readMeta("meta.json");
	const result = metaSchema.safeParse(meta);
	assert.ok(result.success, JSON.stringify(result.error?.issues));

	const listed = (meta.pages as string[]).filter((p) => !p.startsWith("---"));
	const onDisk = pages.map((file) => file.replace(/\.mdx$/, "")).toSorted();
	assert.deepEqual(listed.toSorted(), onDisk);
});

test("internal docs links point at pages that exist", () => {
	const urls = new Set(pages.map(pageUrl));
	for (const file of pages) {
		for (const [, href] of pageText(file).matchAll(/\]\((\/docs[^)#\s]*)(?:#[^)]*)?\)/g)) {
			if (/\.(png|jpe?g|webp|gif)$/.test(href)) continue;
			assert.ok(urls.has(href), `${file} links to missing ${href}`);
		}
	}
});

test("every icon a page names is in the docs icon whitelist", () => {
	const whitelist = readFileSync(path.join(WEB_ROOT, "lib/source.ts"), "utf8");
	for (const file of pages) {
		const icon = pageText(file).match(/^icon:\s*(\S+)/m)?.[1];
		assert.ok(icon, `${file} has no icon`);
		assert.match(whitelist, new RegExp(`^\\t${icon},$`, "m"), `${file}: ${icon} not whitelisted`);
	}
});

test("the docs never name the upstream product, and name ours through BrandName", () => {
	for (const file of pages) {
		// A setting's name in code (`SURFSENSE_PUBLIC_URL`) is an identifier that
		// keeps the upstream spelling on purpose, not the product's name.
		const text = pageText(file).replace(/`[A-Z][A-Z0-9_]*`/g, "``");
		assert.doesNotMatch(text, /surf\s?sense/i, `${file} mentions the upstream name`);
		assert.doesNotMatch(text, new RegExp(brand.name), `${file} hardcodes the product name`);
	}
});

test("the docs name no one server: its address, sign-in and contact come from settings", () => {
	// Every server serves these docs, so one server's host, mail domain or
	// sign-in name here would be wrong on every other (lib/sso-settings,
	// components/docs/single-sign-on.tsx and site-address.tsx fill them in).
	for (const file of pages) {
		const text = pageText(file);
		assert.doesNotMatch(text, /example|soloclef\.com/i, `${file} names one server`);
		assert.doesNotMatch(text, /https?:\/\/(?!localhost[:/])/i, `${file} hardcodes a site address`);
	}
});

test("the pricing page's tables come from the shared catalog", () => {
	const text = pageText("plans-and-credits.mdx");
	for (const tag of ["<PlanTable />", "<PackTable />", "<CreditCostTable />"]) {
		assert.ok(text.includes(tag), `plans-and-credits.mdx lost ${tag}`);
	}
	// The worked example in the prose must match the catalog's page prices.
	const cloud = DEFAULT_CATALOG.prices.find((p) => p.action === "cloud_page");
	const local = DEFAULT_CATALOG.prices.find((p) => p.action === "local_page");
	assert.equal(local?.credits, 0);
	assert.ok(text.includes(`${20 * (cloud?.credits ?? 0)} credits with the`));
	const chat = DEFAULT_CATALOG.prices.filter((p) => p.action.startsWith("chat_"));
	assert.deepEqual(
		chat.map((p) => p.credits),
		[1, 5]
	);
});
