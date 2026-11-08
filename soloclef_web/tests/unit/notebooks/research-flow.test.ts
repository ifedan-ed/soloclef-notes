import assert from "node:assert/strict";
import test from "node:test";
import {
	canImport,
	importItemsFor,
	initialSelection,
	resultsSelectState,
	selectAllResults,
	toggleResult,
} from "@/features/notebooks/lib/research-selection";
import { idleResearch, researchReducer } from "@/features/notebooks/lib/research-state";
import type { ResearchRequest, ResearchResult } from "@/features/notebooks/model/research";

function result(id: string, url = `https://example.com/${id}`): ResearchResult {
	return { id, url, title: `Title ${id}`, snippet: `Snippet ${id}`, source: "web", meta: null };
}

const request: ResearchRequest = { query: "iron", source: "web", mode: "fast" };

test("a search moves through its phases to results", () => {
	let state = researchReducer(idleResearch, { type: "start", request });
	assert.equal(state.status, "searching");
	state = researchReducer(state, {
		type: "event",
		event: { type: "phase", phase: "reading", progress: 60 },
	});
	state = researchReducer(state, {
		type: "event",
		event: { type: "phase", phase: "searching", progress: 20 },
	});
	assert.equal(state.status === "searching" && state.progress, 60, "progress never goes back");
	state = researchReducer(state, {
		type: "event",
		event: { type: "results", results: [result("a")] },
	});
	state = researchReducer(state, { type: "event", event: { type: "done" } });
	assert.deepEqual(state, {
		status: "results",
		request,
		results: [result("a")],
		providers: [],
	});
});

test("an error event or a failed request ends in an error that remembers the request", () => {
	const searching = researchReducer(idleResearch, { type: "start", request });
	assert.deepEqual(
		researchReducer(searching, {
			type: "event",
			event: { type: "error", message: "quota", code: "out_of_credit" },
		}),
		{ status: "error", request, message: "quota", code: "out_of_credit", source: null }
	);
	assert.deepEqual(researchReducer(searching, { type: "failed", message: null }), {
		status: "error",
		request,
		message: null,
		code: null,
	});
});

test("a stream that closes without done still shows what arrived; clear resets", () => {
	const searching = researchReducer(idleResearch, { type: "start", request });
	assert.deepEqual(researchReducer(searching, { type: "closed" }), {
		status: "results",
		request,
		results: [],
		providers: [],
	});
	assert.deepEqual(researchReducer(searching, { type: "clear" }), idleResearch);
	const done = researchReducer(searching, { type: "closed" });
	assert.equal(researchReducer(done, { type: "event", event: { type: "done" } }), done);
});

test("results arrive checked, up to the import limit", () => {
	const many = Array.from({ length: 60 }, (_, i) => result(String(i)));
	assert.equal(initialSelection(many).size, 50);
	assert.equal(initialSelection([result("a")]).has("a"), true);
});

test("select all flips between everything and nothing", () => {
	const results = [result("a"), result("b")];
	assert.equal(resultsSelectState(results, new Set()), "none");
	assert.equal(resultsSelectState(results, new Set(["a"])), "some");
	assert.equal(resultsSelectState(results, new Set(["a", "b"])), "all");
	assert.deepEqual([...selectAllResults(results, "some")], ["a", "b"]);
	assert.deepEqual([...selectAllResults(results, "all")], []);
	assert.deepEqual([...toggleResult(new Set(["a"]), "a")], []);
	assert.deepEqual([...toggleResult(new Set(["a"]), "b")], ["a", "b"]);
});

test("the import body keeps display order, drops repeated URLs and unchecked rows", () => {
	const results = [result("a"), result("b"), result("c", "https://example.com/a")];
	assert.deepEqual(importItemsFor(results, new Set(["c", "a"])), [
		{ url: "https://example.com/a", title: "Title a", snippet: "Snippet a", source: "web" },
	]);
	assert.deepEqual(
		importItemsFor(results, new Set(["b"])).map((item) => item.title),
		["Title b"]
	);
	assert.equal(canImport([]), false);
	assert.equal(canImport(importItemsFor(results, new Set(["a"]))), true);
	const tooMany = Array.from({ length: 51 }, (_, i) => result(String(i)));
	assert.equal(canImport(importItemsFor(tooMany, new Set(tooMany.map((r) => r.id)))), false);
});

test("a paper result carries its ids into the import body", () => {
	const paper = {
		pmid: "35721052",
		doi: "10.3389/fmed.2022.920549",
		authors: ["Milési C"],
		categories: [],
		badges: ["pubmed", "pmc"],
		open_access: true,
	};
	const results: ResearchResult[] = [{ ...result("p"), source: "pubmed", paper }];
	assert.deepEqual(importItemsFor(results, new Set(["p"])), [
		{
			url: "https://example.com/p",
			title: "Title p",
			snippet: "Snippet p",
			source: "pubmed",
			paper,
		},
	]);
});
