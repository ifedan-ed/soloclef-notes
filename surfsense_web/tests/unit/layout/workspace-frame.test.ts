import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { workspacePageFrame } from "@/components/layout/providers/workspace-page-frame";
import {
	DESKTOP_LAYOUT_COLUMNS,
	DESKTOP_LAYOUT_TRACKS,
} from "@/components/layout/ui/shell/desktop-layout-columns";

test("the desktop grid has one track per column and no spare track", () => {
	// Rail, workspace: a spare track sat empty beside every page, and the
	// second sidebar that had a track of its own is gone.
	assert.equal(DESKTOP_LAYOUT_TRACKS.length, 2);
	assert.equal(DESKTOP_LAYOUT_COLUMNS, `grid-cols-[${DESKTOP_LAYOUT_TRACKS.join("_")}]`);
	const flexible = DESKTOP_LAYOUT_TRACKS.filter((track) => track.includes("fr"));
	assert.deepEqual(flexible, ["minmax(0,1fr)"]);
	assert.equal(DESKTOP_LAYOUT_TRACKS.at(-1), "minmax(0,1fr)");
});

test("the shell renders exactly the columns the grid declares", () => {
	const source = readFileSync(
		join(process.cwd(), "components/layout/ui/shell/LayoutShell.tsx"),
		"utf8"
	);
	assert.match(source, /DESKTOP_LAYOUT_COLUMNS/);
	assert.doesNotMatch(source, /grid-cols-\[auto_auto/);
});

test("every dashboard page but a notebook sits in the workspace panel", () => {
	for (const path of [
		"/dashboard/1/notebooks",
		"/dashboard/1/user-settings/profile",
		"/dashboard/1/workspace-settings/connectors",
		"/dashboard/1/workspace-settings/automations",
		"/dashboard/1/automations/7",
		"/dashboard/1/logs",
		"/dashboard/1/team",
	]) {
		const frame = workspacePageFrame(path);
		assert.equal(frame.focusMode, false, path);
		assert.match(frame.contentClassName ?? "", /max-w-/, path);
	}
	assert.deepEqual(workspacePageFrame("/dashboard/1/notebooks/12"), { focusMode: true });
});

test("the notebooks home gets the wide frame, settings the reading width", () => {
	assert.match(workspacePageFrame("/dashboard/1/notebooks/")?.contentClassName ?? "", /max-w-6xl/);
	assert.match(
		workspacePageFrame("/dashboard/1/user-settings/profile").contentClassName ?? "",
		/max-w-5xl/
	);
});
