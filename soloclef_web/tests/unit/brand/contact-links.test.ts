import assert from "node:assert/strict";
import test from "node:test";
import {
	CONTACT_HREF,
	FOOTER_COLUMNS,
	NAV_LINKS,
	shownLinks,
} from "@/components/site/site-content";

// Run with: pnpm exec tsx --test tests/unit/brand/contact-links.test.ts

test("Contact is in the menu and footer when the server publishes an address", () => {
	assert.ok(shownLinks(NAV_LINKS, true).some((link) => link.href === CONTACT_HREF));
	const footer = FOOTER_COLUMNS.flatMap((column) => shownLinks(column.links, true));
	assert.ok(footer.some((link) => link.href === CONTACT_HREF));
});

test("Contact is left out of the menu and footer when there is no address, and nothing else is", () => {
	const nav = shownLinks(NAV_LINKS, false);
	assert.ok(!nav.some((link) => link.href === CONTACT_HREF));
	assert.equal(nav.length, NAV_LINKS.length - 1);
	for (const column of FOOTER_COLUMNS) {
		const shown = shownLinks(column.links, false);
		assert.deepEqual(
			shown,
			column.links.filter((link) => link.href !== CONTACT_HREF)
		);
	}
	// Privacy and Terms stay: they carry the contact page in their own text.
	const legal = FOOTER_COLUMNS.flatMap((column) => shownLinks(column.links, false));
	assert.ok(legal.some((link) => link.href === "/privacy"));
	assert.ok(legal.some((link) => link.href === "/terms"));
});
