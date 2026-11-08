import assert from "node:assert/strict";
import test from "node:test";

import { AppError, AuthorizationError, NotFoundError } from "@/lib/error";
import {
	isRemovedWorkspaceError,
	keyMentionsWorkspace,
	markWorkspaceRemoved,
	pickNextWorkspace,
	removedWorkspaceIds,
	unmarkWorkspaceRemoved,
	WORKSPACE_ACCESS_DENIED,
	withVisit,
} from "@/lib/workspace-removal";

const spaces = [{ id: 1 }, { id: 2 }, { id: 3 }];

test("after a removal the most recently opened remaining workspace is next", () => {
	assert.equal(pickNextWorkspace(spaces, [3], [3, 2, 1]), 2);
});

test("with no usable history the first remaining workspace is next", () => {
	assert.equal(pickNextWorkspace(spaces, [1], []), 2);
	// Visits to workspaces no longer listed are skipped.
	assert.equal(pickNextWorkspace(spaces, [1], [9, 8]), 2);
});

test("the removed workspace is never picked, even if listed first or visited last", () => {
	assert.equal(pickNextWorkspace([{ id: 5 }, { id: 6 }], [5], [5]), 6);
});

test("nothing is next when nothing is left", () => {
	assert.equal(pickNextWorkspace([{ id: 5 }], [5], [5]), null);
	assert.equal(pickNextWorkspace([], [], []), null);
});

test("a visit moves to the front without duplicates", () => {
	assert.deepEqual(withVisit([1, 2, 3], 3), [3, 1, 2]);
	assert.deepEqual(withVisit([], 4), [4]);
	assert.equal(
		withVisit(
			Array.from({ length: 40 }, (_, i) => i + 1),
			99
		).length,
		20
	);
});

test("keys name a workspace as a number or a string", () => {
	assert.equal(keyMentionsWorkspace(["workspaces", "7"], [7]), true);
	assert.equal(keyMentionsWorkspace(["notebooks", 7, "list"], [7]), true);
	assert.equal(keyMentionsWorkspace(["workspaces", "8"], [7]), false);
	assert.equal(keyMentionsWorkspace(undefined, [7]), false);
	assert.equal(keyMentionsWorkspace(["workspaces", "7"], []), false);
});

test("403/404s about a removed workspace are quiet; everything else still shows", () => {
	const forbidden = new AuthorizationError(WORKSPACE_ACCESS_DENIED, 403);
	const missing = new NotFoundError("Workspace not found", 404);
	const other = new AuthorizationError("You don't have permission to delete", 403);
	const server = new AppError("boom", 500);

	// Nothing removed in this tab: every error shows.
	assert.equal(isRemovedWorkspaceError(forbidden, ["workspaces", "7"], []), false);

	assert.equal(isRemovedWorkspaceError(forbidden, ["workspaces", "7"], [7]), true);
	assert.equal(isRemovedWorkspaceError(missing, ["notebooks", 7], [7]), true);
	// The backend's "no access to this workspace" is quiet under any key.
	assert.equal(isRemovedWorkspaceError(forbidden, ["members", "me"], [7]), true);
	// Another workspace's permission refusal is real.
	assert.equal(isRemovedWorkspaceError(other, ["workspaces", "8"], [7]), false);
	assert.equal(isRemovedWorkspaceError(server, ["workspaces", "7"], [7]), false);
	assert.equal(isRemovedWorkspaceError(new Error("x"), ["workspaces", "7"], [7]), false);
});

test("a failed delete takes its mark back", () => {
	markWorkspaceRemoved(42);
	assert.ok(removedWorkspaceIds().includes(42));
	unmarkWorkspaceRemoved(42);
	assert.ok(!removedWorkspaceIds().includes(42));
});
