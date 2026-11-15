import assert from "node:assert/strict";
import test from "node:test";
import { numberCitationNodes } from "@/components/editor/utils/citation-nodes";
import {
	citationKey,
	citationsInOrder,
	numberCitations,
	splitLabels,
} from "@/lib/citations/citation-numbering";

// Run with: pnpm exec tsx --test tests/unit/citations/citation-numbering.test.ts

const numbersOf = (texts: string[], labels = false) =>
	Object.fromEntries(numberCitations(texts, { labels }).numbers);

test("sources are numbered 1, 2, 3 in order of first appearance", () => {
	assert.deepEqual(numbersOf(["A [citation:812]. B [citation:90]. C [citation:5]."]), {
		"chunk:812": 1,
		"chunk:90": 2,
		"chunk:5": 3,
	});
});

test("a source cited again keeps its number", () => {
	const { order, numbers } = numberCitations([
		"[citation:7] [citation:3] [citation:7] [citation:3]",
	]);
	assert.deepEqual(order, ["chunk:7", "chunk:3"]);
	assert.equal(numbers.get("chunk:7"), 1);
});

test("web addresses are numbered in the same sequence as passages", () => {
	assert.deepEqual(
		numbersOf(["One [citation:4], a page [citation:https://example.org/a?b=1], two [citation:9]."]),
		{ "chunk:4": 1, "url:https://example.org/a?b=1": 2, "chunk:9": 3 }
	);
});

test("numbering runs across the answer's text parts, in order", () => {
	assert.deepEqual(numbersOf(["first [citation:2]", "second [citation:1] [citation:2]"]), {
		"chunk:2": 1,
		"chunk:1": 2,
	});
});

test("a comma marker numbers each id left to right", () => {
	assert.deepEqual(numbersOf(["x [citation:30, 10] y [citation:10]"]), {
		"chunk:30": 1,
		"chunk:10": 2,
	});
});

test("markers inside code are not citations", () => {
	assert.deepEqual(numbersOf(["`[citation:1]` then\n```\n[citation:2]\n```\n real [citation:3]"]), {
		"chunk:3": 1,
	});
});

test("legacy doc and negative markers get no number", () => {
	assert.deepEqual(numbersOf(["a [citation:doc-4] b [citation:-2] c [citation:8]"]), {
		"chunk:8": 1,
	});
	assert.equal(citationKey({ kind: "chunk", chunkId: 4, isDocsChunk: true }), null);
});

test("streamed labels are numbered only while streaming", () => {
	const streamed = ["Fever [17] is common [4], and [17] again; code `a[2]` is not."];
	assert.deepEqual(numbersOf(streamed), {});
	assert.deepEqual(numbersOf(streamed, true), { "label:17": 1, "label:4": 2 });
});

test("numbers do not change when the saved text replaces the streamed one", () => {
	// The registry numbered the thread's sources: label 17 is chunk 812,
	// label 4 is a page. The saved text holds markers in the same places.
	const streamed = "Fever [17] is common [ 4 ], see [17].";
	const saved = "Fever [citation:812] is common [citation:https://x.org/p], see [citation:812].";
	const before = numberCitations([streamed], { labels: true });
	const after = numberCitations([saved]);
	assert.deepEqual([...before.numbers.values()], [...after.numbers.values()]);
	assert.equal(before.numbers.get("label:17"), after.numbers.get("chunk:812"));
	assert.equal(before.numbers.get("label:4"), after.numbers.get("url:https://x.org/p"));
});

test("a half-streamed answer keeps the numbers it has already shown", () => {
	const full = "A [9] B [2] C [9] D [5]";
	let shown = new Map<string, number>();
	for (let end = 1; end <= full.length; end++) {
		const now = numberCitations([full.slice(0, end)], { labels: true }).numbers;
		for (const [key, number] of shown) assert.equal(now.get(key), number, `at ${end}`);
		shown = new Map(now);
	}
});

test("labels and markers read in one pass keep reading order", () => {
	assert.deepEqual(
		citationsInOrder("[3] then [citation:12] then [1]", true).map((c) => citationKey(c)),
		["label:3", "chunk:12", "label:1"]
	);
});

test("a bracket that is not a label stays text", () => {
	assert.deepEqual(splitLabels("[a] [1, 2] [12]"), ["[a] [1, 2] ", { kind: "label", label: 12 }]);
});

test("a note's chips are numbered in reading order across its blocks", () => {
	const chip = (chunkId: number) => ({
		type: "citation",
		kind: "chunk",
		chunkId,
		rawText: "",
		children: [{ text: "" }],
	});
	const value = [
		{ type: "p", children: [{ text: "a" }, chip(50), { text: "" }] },
		{ type: "ul", children: [{ type: "li", children: [chip(7), chip(50)] }] },
	] as never;
	const numbering = numberCitationNodes(value);
	assert.deepEqual(numbering.order, ["chunk:50", "chunk:7"]);
	// The same tree is numbered once.
	assert.equal(numberCitationNodes(value), numbering);
});
