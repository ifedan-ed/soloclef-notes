import assert from "node:assert/strict";
import test from "node:test";
import {
	citedRects,
	findTextOnPage,
	type PlacedTextItem,
	pageToOpen,
} from "@/features/file-viewers/pdf-cited-text";

const items = (...strs: string[]) => strs.map((str) => ({ str }));

test("the page to open must be a real page of the document", () => {
	assert.equal(pageToOpen(12, 40), 12);
	assert.equal(pageToOpen(1, 40), 1);
	assert.equal(pageToOpen(41, 40), null);
	assert.equal(pageToOpen(0, 40), null);
	assert.equal(pageToOpen(2.5, 40), null);
	assert.equal(pageToOpen(undefined, 40), null);
	assert.equal(pageToOpen(null, 40), null);
});

test("a markdown chunk is found among the page's text runs", () => {
	const page = items(
		"Running header, volume 12",
		"Treatment",
		"Intravenous immunoglobulin is given",
		"within ten days of fever onset.",
		"Aspirin is continued until",
		"Unrelated footer"
	);
	const chunk =
		"## Treatment\n\n**Intravenous immunoglobulin** is given within ten days of fever onset. Aspirin is continued until";

	assert.deepEqual(findTextOnPage(page, chunk), { first: 1, last: 4 });
});

test("a chunk that runs on to the next page is marked to the page end", () => {
	const page = items("Echocardiography screens for", "coronary artery aneurysms at");
	const chunk =
		"Echocardiography screens for coronary artery aneurysms at diagnosis and six weeks.";

	assert.deepEqual(findTextOnPage(page, chunk), { first: 0, last: 1 });
});

test("a word hyphenated over a line break does not stop the rest matching", () => {
	const page = items("Give azithro-", "mycin 10 mg per kg once daily for three days.");
	const chunk = "Give azithromycin 10 mg per kg once daily for three days.";

	assert.deepEqual(findTextOnPage(page, chunk), { first: 1, last: 1 });
});

test("text that is not on the page is not found", () => {
	assert.equal(
		findTextOnPage(items("Something else entirely here."), "Not on this page at all"),
		null
	);
	assert.equal(findTextOnPage([], "anything"), null);
	assert.equal(findTextOnPage(items("text"), ""), null);
});

test("marked rectangles are page percentages, so they follow zoom", () => {
	const placed: PlacedTextItem[] = [
		{ str: "Cited", transform: [10, 0, 0, 10, 100, 700], width: 50, height: 10 },
		{ str: " ", transform: [10, 0, 0, 10, 150, 700], width: 5, height: 10 },
	];
	// A 600 x 800 page at scale 1: PDF y grows upwards, the page box downwards.
	const viewport = {
		width: 600,
		height: 800,
		convertToViewportRectangle: ([x1, y1, x2, y2]: number[]) => [x1, 800 - y1, x2, 800 - y2],
	};

	const rects = citedRects(placed, { first: 0, last: 1 }, viewport);

	assert.equal(rects.length, 1);
	assert.deepEqual(rects[0], {
		left: (100 / 600) * 100,
		top: (90 / 800) * 100,
		width: (50 / 600) * 100,
		height: (10 / 800) * 100,
	});
});
