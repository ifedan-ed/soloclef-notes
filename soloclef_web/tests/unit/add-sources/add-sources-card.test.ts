import assert from "node:assert/strict";
import test from "node:test";
import {
	addSourcesSummary,
	itemLabel,
	parseAddSourcesCard,
	withLiveReading,
} from "../../../features/add-sources/add-sources-card";

// The payload as the server's allowlist sends it (soloclef_assistant/addsources, Card).
const sent = {
	notebook_id: 12,
	items: [
		{ status: "added", document_id: 7, title: "Tidal heights by port", reading: true },
		{ status: "already_a_source", document_id: 3, title: "Held already" },
		{ status: "not_added", reason: "PubMed has no paper with this id.", ref: "99999999" },
	],
};

test("a finished call's payload is read, and the line counts what was added", () => {
	const card = parseAddSourcesCard(sent);
	assert.ok(card);
	assert.equal(card.items.length, 3);
	assert.equal(addSourcesSummary(card), "Added 1 source");
	assert.equal(
		addSourcesSummary({ ...card, items: [card.items[0], card.items[0]] }),
		"Added 2 sources"
	);
	assert.equal(addSourcesSummary({ ...card, items: [card.items[2]] }), "Added no sources");
});

test("a row without a title is labelled with the identity that was sent", () => {
	const card = parseAddSourcesCard(sent);
	assert.ok(card);
	assert.equal(itemLabel(card.items[0]), "Tidal heights by port");
	assert.equal(itemLabel(card.items[2]), "99999999");
});

test("anything that is not the payload leaves the call in the trace", () => {
	for (const result of [
		undefined,
		null,
		"Added 2 sources",
		{ status: "completed", length: 120 },
		{ notebook_id: 12, items: [] },
		{ notebook_id: 12, items: [{ status: "imported" }] },
	]) {
		assert.equal(parseAddSourcesCard(result), null, JSON.stringify(result));
	}
});

test("reading follows the notebook's sources as they are now", () => {
	const card = parseAddSourcesCard(sent);
	assert.ok(card);
	const live = withLiveReading(card, [
		{ kind: "document", target_id: 7, status: "ready" },
		{ kind: "document", target_id: 3, status: "processing" },
	]);
	assert.equal(live.items[0].reading, false);
	assert.equal(live.items[1].reading, true);
	// Not listed (yet): the saved word stands.
	assert.equal(withLiveReading(card, []).items[0].reading, true);
	assert.equal(withLiveReading(card, undefined), card);
});
