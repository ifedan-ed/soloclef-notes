import assert from "node:assert/strict";
import test from "node:test";
import { retryRequest } from "@/features/notebooks/lib/studio-retry";
import type { NotebookSource, StudioOutput } from "@/features/notebooks/model/notebook";

function output(source_ids: number[] | null): StudioOutput {
	return {
		id: 9,
		kind: "quiz",
		title: "Quiz: Iron",
		status: "failed",
		source_count: 2,
		source_ids,
		options: { kind: "quiz", focus: "iron", count: "fewer", difficulty: "hard" },
		created_at: "2026-11-15T10:00:00Z",
	};
}

function source(id: number): NotebookSource {
	return {
		id,
		kind: "document",
		target_id: id * 10,
		title: `S${id}`,
		enabled: true,
		added_at: "2026-11-15T09:00:00Z",
	};
}

test("a retry sends the options and the sources the output was asked for", () => {
	assert.deepEqual(retryRequest(output([2, 5]), [source(1), source(2), source(5)], "r1"), {
		kind: "quiz",
		focus: "iron",
		count: "fewer",
		difficulty: "hard",
		title: "Quiz: Iron",
		request_id: "r1",
		source_ids: [2, 5],
	});
});

test("a source removed since is left out", () => {
	assert.deepEqual(retryRequest(output([2, 5]), [source(1), source(5)], "r1").source_ids, [5]);
});

test("with every source gone the empty list is sent, for the server to refuse by name", () => {
	assert.deepEqual(retryRequest(output([2]), [source(1)], "r1").source_ids, []);
});

test("an output that recorded no sources sends none, so the ticked ones are read", () => {
	assert.equal("source_ids" in retryRequest(output(null), [source(1)], "r1"), false);
});

test("before the sources are read the recorded ones are sent as they are", () => {
	assert.deepEqual(retryRequest(output([2, 5]), undefined, "r1").source_ids, [2, 5]);
});
