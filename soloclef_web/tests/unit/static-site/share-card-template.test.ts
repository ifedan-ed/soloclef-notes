import assert from "node:assert/strict";
import test from "node:test";
import {
	CLOSE,
	cardTemplate,
	OPEN,
	outputKindLabels,
	prelude,
	SUMMARY_PATH,
} from "../../../scripts/share-card-template";
import { ROUTES_CADDY } from "../caddy-routes";

// Run with: pnpm exec tsx --test tests/unit/static-site/share-card-template.test.ts

// The head Next.js writes for app/public/[token] (the tags that matter).
const SHELL = [
	"<!DOCTYPE html><html><head>",
	"<title>Soloclef Notes</title>",
	'<meta name="description" content="Notes &amp; more"/>',
	'<meta name="robots" content="noindex, nofollow"/>',
	'<meta property="og:title" content="Soloclef Notes"/>',
	'<meta property="og:description" content="Notes &amp; more"/>',
	'<meta property="og:site_name" content="Soloclef Notes"/>',
	'<meta property="og:type" content="website"/>',
	'<meta name="twitter:title" content="Soloclef Notes"/>',
	'<meta name="twitter:description" content="Notes &amp; more"/>',
	"</head><body>{{ not a template }}</body></html>",
].join("");

const options = {
	brandName: "Soloclef Notes",
	siteDescription: 'Notes "and" more',
	kinds: { flashcards: "Flashcards", mindmap: "Mind map" },
};

test("every card tag becomes the template's value, HTML-escaped", () => {
	const html = cardTemplate(SHELL, options);
	assert.match(html, /<title>\[\[% \$title \| html %\]\]<\/title>/);
	for (const tag of ["og:title", "twitter:title"]) {
		assert.ok(html.includes(`"${tag}" content="${OPEN} $title | html ${CLOSE}"`), tag);
	}
	for (const tag of ["description", "og:description", "twitter:description"]) {
		assert.ok(html.includes(`"${tag}" content="${OPEN} $description | html ${CLOSE}"`), tag);
	}
	assert.ok(html.includes(`"og:type" content="${OPEN} $type ${CLOSE}"`));
	// Untouched: the site's name, robots, and anything that only looks like a template.
	assert.ok(html.includes('"og:site_name" content="Soloclef Notes"'));
	assert.ok(html.includes("{{ not a template }}"));
	assert.ok(html.startsWith(prelude(options.brandName, options.siteDescription, options.kinds)));
});

test("the prelude asks the share card path for the token Caddy captured", () => {
	const text = prelude(options.brandName, options.siteDescription, options.kinds);
	assert.ok(text.includes(`placeholder "http.regexp.publicLink.1"`));
	assert.ok(text.includes(`printf "${SUMMARY_PATH}%s" $token`));
	// The words are Go string literals: quotes in a setting cannot end one.
	assert.ok(text.includes(`$description := "Notes \\"and\\" more"`));
	assert.ok(text.includes(`dict "flashcards" "Flashcards" "mindmap" "Mind map"`));
	// Every action trims, so the prelude prints nothing before <!DOCTYPE html>.
	for (const action of text.split(CLOSE).filter(Boolean)) {
		assert.ok(action.startsWith(`${OPEN}- `) && action.endsWith(" -"), action);
	}
});

test("Caddy's routes use the same delimiters, matcher name and path", () => {
	assert.ok(ROUTES_CADDY.includes(`between ${OPEN} ${CLOSE}`));
	assert.ok(ROUTES_CADDY.includes("@publicLink path_regexp publicLink "));
	assert.ok(ROUTES_CADDY.includes(`handle_path ${SUMMARY_PATH}* {`));
});

test("a page whose metadata changed stops the build instead of shipping a blank card", () => {
	assert.throws(
		() => cardTemplate(SHELL.replace(/<meta property="og:type"[^>]*>/, ""), options),
		/og:type/
	);
	assert.throws(() => cardTemplate(`${SHELL}[[%`, options), /delimiters/);
});

test("the kinds are the Studio's own labels", () => {
	const labels = outputKindLabels();
	assert.ok(Object.keys(labels).length >= 5);
	for (const label of Object.values(labels)) assert.ok(label.trim());
});
