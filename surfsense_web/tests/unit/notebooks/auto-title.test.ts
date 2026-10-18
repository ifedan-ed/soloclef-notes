import assert from "node:assert/strict";
import test from "node:test";
import {
	notebookRefetchInterval,
	notebooksRefetchInterval,
	readySourceCount,
	startTitleWatch,
	TITLE_WATCH_MS,
	withNotebookName,
} from "@/features/notebooks/lib/auto-title";
import { notebookSchema } from "@/features/notebooks/model/notebook";

const NOW = 1_800_000_000_000;

function notebook(overrides: Record<string, unknown> = {}) {
	return notebookSchema.parse({
		id: 7,
		workspace_id: 1,
		title: "Untitled notebook",
		icon: "📓",
		created_at: "2026-09-25T00:00:00Z",
		updated_at: "2026-09-25T00:00:00Z",
		source_count: 0,
		enabled_source_count: 0,
		title_source: "auto",
		...overrides,
	});
}

test("older servers without the fields read as user-named, never polled", () => {
	const legacy = notebookSchema.parse({
		id: 1,
		workspace_id: 1,
		title: "Untitled notebook",
		icon: null,
		created_at: "2026-09-25T00:00:00Z",
		updated_at: "2026-09-25T00:00:00Z",
		source_count: 0,
		enabled_source_count: 0,
	});
	assert.equal(legacy.title_source, "user");
	assert.equal(legacy.title_pending, false);
	assert.equal(startTitleWatch(legacy, NOW), null);
	assert.equal(notebookRefetchInterval(legacy, null, NOW), false);
});

test("counts only ready sources", () => {
	assert.equal(
		readySourceCount([{ status: "ready" }, { status: "processing" }, { status: null }]),
		1
	);
	assert.equal(readySourceCount(undefined), 0);
});

test("a watch polls until the name changes or the window closes", () => {
	const unnamed = notebook();
	const watch = startTitleWatch(unnamed, NOW);
	assert.deepEqual(watch, { until: NOW + TITLE_WATCH_MS, fromTitle: "Untitled notebook" });
	assert.equal(notebookRefetchInterval(unnamed, watch, NOW + 1000), 3000);
	// The name arrived: stop.
	const named = notebook({ title: "Complications of Premature Delivery" });
	assert.equal(notebookRefetchInterval(named, watch, NOW + 4000), false);
	// Nothing came in time: stop too.
	assert.equal(notebookRefetchInterval(unnamed, watch, NOW + TITLE_WATCH_MS + 1), false);
});

test("a renamed notebook is never watched or polled", () => {
	const renamed = notebook({ title: "Mine", title_source: "user", title_pending: true });
	assert.equal(startTitleWatch(renamed, NOW), null);
	const staleWatch = { until: NOW + 10_000, fromTitle: "Untitled notebook" };
	assert.equal(notebookRefetchInterval(renamed, staleWatch, NOW), false);
	assert.equal(notebooksRefetchInterval([renamed]), false);
});

test("the server's pending hint keeps the page and home looking", () => {
	const pending = notebook({ title_pending: true });
	assert.equal(notebookRefetchInterval(pending, null, NOW), 5000);
	assert.equal(notebooksRefetchInterval([notebook(), pending]), 5000);
	assert.equal(notebooksRefetchInterval([notebook()]), false);
	assert.equal(notebooksRefetchInterval(undefined), false);
});

test("the list takes the new name and icon, and is untouched otherwise", () => {
	const list = [notebook({ id: 1, title: "Other" }), notebook()];
	const same = withNotebookName(list, notebook());
	assert.equal(same, list);
	const renamed = withNotebookName(list, notebook({ title: "Neonatal Sepsis", icon: "🩺" }));
	assert.notEqual(renamed, list);
	assert.equal(renamed?.[0], list[0]);
	assert.equal(renamed?.[1].title, "Neonatal Sepsis");
	assert.equal(renamed?.[1].icon, "🩺");
	assert.equal(withNotebookName(undefined, notebook()), undefined);
});
