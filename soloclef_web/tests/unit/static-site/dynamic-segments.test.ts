import assert from "node:assert/strict";
import test from "node:test";
import {
	DYNAMIC_PREFIXES,
	idAfter,
	STATIC_PLACEHOLDER,
	segmentAfter,
} from "@/lib/routing/dynamic-segments";
import { ROUTES_CADDY } from "../caddy-routes";

// Run with: pnpm exec tsx --test tests/unit/static-site/dynamic-segments.test.ts

test("a dynamic page reads its value from the address", () => {
	assert.equal(segmentAfter("/dashboard/notebooks/12", DYNAMIC_PREFIXES.notebook), "12");
	assert.equal(segmentAfter("/dashboard/automations/7/edit", DYNAMIC_PREFIXES.automation), "7");
	assert.equal(segmentAfter("/public/abc-DEF_123", DYNAMIC_PREFIXES.publicLink), "abc-DEF_123");
	assert.equal(segmentAfter("/public/a%2Fb", DYNAMIC_PREFIXES.publicLink), "a/b");
});

test("the placeholder the file was written for, and other pages, name nothing", () => {
	assert.equal(
		segmentAfter(`/dashboard/notebooks/${STATIC_PLACEHOLDER}`, DYNAMIC_PREFIXES.notebook),
		null
	);
	assert.equal(segmentAfter("/dashboard/notebooks", DYNAMIC_PREFIXES.notebook), null);
	assert.equal(segmentAfter("/dashboard/notebooks/", DYNAMIC_PREFIXES.notebook), null);
	assert.equal(segmentAfter("/dashboard/notes/3", DYNAMIC_PREFIXES.notebook), null);
	assert.equal(segmentAfter(null, DYNAMIC_PREFIXES.notebook), null);
	assert.equal(segmentAfter("/public/%E0%A4%A", DYNAMIC_PREFIXES.publicLink), null, "bad escape");
});

test("an id is a positive whole number or nothing", () => {
	assert.equal(idAfter("/dashboard/notebooks/42", DYNAMIC_PREFIXES.notebook), 42);
	assert.equal(idAfter("/dashboard/notebooks/0", DYNAMIC_PREFIXES.notebook), null);
	assert.equal(idAfter("/dashboard/notebooks/4.5", DYNAMIC_PREFIXES.notebook), null);
	assert.equal(idAfter("/dashboard/notebooks/abc", DYNAMIC_PREFIXES.notebook), null);
	assert.equal(
		idAfter("/dashboard/notebooks/99999999999999999999", DYNAMIC_PREFIXES.notebook),
		null
	);
});

test("Caddy answers every dynamic prefix with the placeholder's file", () => {
	// routes.caddy rewrites each prefix to its "_" file; a prefix added here
	// and not there would be a 404 for every address under it.
	for (const prefix of Object.values(DYNAMIC_PREFIXES)) {
		assert.ok(
			ROUTES_CADDY.includes(` ${prefix}${STATIC_PLACEHOLDER}`),
			`routes.caddy has no rewrite to ${prefix}${STATIC_PLACEHOLDER}`
		);
	}
});
