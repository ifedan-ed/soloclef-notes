import assert from "node:assert/strict";
import test from "node:test";
import { noteFromAnswer } from "@/features/notebooks/lib/note-from-answer";
import {
	formatDuration,
	isInFlight,
	outputsRefetchInterval,
} from "@/features/notebooks/lib/output-meta";
import { relativeTime } from "@/features/notebooks/lib/relative-time";
import {
	enabledCount,
	nextSelectAllValue,
	selectAllState,
	withToggled,
} from "@/features/notebooks/lib/source-selection";
import type { NotebookSource } from "@/features/notebooks/model/notebook";

function source(id: number, enabled: boolean): NotebookSource {
	return {
		id,
		kind: "document",
		target_id: id,
		title: `Doc ${id}`,
		enabled,
		added_at: "2026-01-01T00:00:00Z",
	};
}

test("select-all reflects all, none and some", () => {
	assert.equal(selectAllState([]), "none");
	assert.equal(selectAllState([source(1, true), source(2, true)]), "all");
	assert.equal(selectAllState([source(1, false)]), "none");
	assert.equal(selectAllState([source(1, true), source(2, false)]), "some");
	assert.equal(nextSelectAllValue("some"), true);
	assert.equal(nextSelectAllValue("all"), false);
});

test("toggles apply to one source or to all", () => {
	const sources = [source(1, true), source(2, false)];
	assert.deepEqual(
		withToggled(sources, 2, true).map((s) => s.enabled),
		[true, true]
	);
	assert.deepEqual(
		withToggled(sources, "all", false).map((s) => s.enabled),
		[false, false]
	);
	assert.equal(enabledCount(sources), 1);
});

test("durations read like a player", () => {
	assert.equal(formatDuration(487), "8:07");
	assert.equal(formatDuration(3725), "1:02:05");
	assert.equal(formatDuration(-3), "0:00");
});

test("the studio list polls only while something is generating", () => {
	assert.equal(isInFlight("queued"), true);
	assert.equal(outputsRefetchInterval(["ready", "failed"]), false);
	assert.equal(outputsRefetchInterval(["ready", "running"]), 4000);
});

test("a saved answer loses its citation tokens and gets a title", () => {
	const note = noteFromAnswer(
		"## Iron deficiency\n\nFerritin is low [citation:12]. Treat early [citation:doc-4, 5]."
	);
	assert.equal(note.title, "Iron deficiency");
	assert.equal(note.content, "## Iron deficiency\n\nFerritin is low. Treat early.");
});

test("relative time is localized", () => {
	const now = new Date("2026-09-23T12:00:00Z");
	assert.equal(relativeTime("2026-09-20T12:00:00Z", "en", now), "3 days ago");
	assert.equal(relativeTime("2026-09-23T11:59:30Z", "en", now), "now");
	assert.match(relativeTime("2026-09-20T12:00:00Z", "es", now), /hace 3 días/);
});
