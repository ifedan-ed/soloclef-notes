import assert from "node:assert/strict";
import test from "node:test";
import type { ReactElement } from "react";
import {
	NotebookThreadPane,
	type ThreadViewProps,
} from "@/features/notebooks/ui/chat/notebook-thread-pane";

function StubView(_props: ThreadViewProps) {
	return null;
}

const noop = () => undefined;

function pane(threadId: number) {
	return NotebookThreadPane({
		view: StubView,
		workspaceId: 1,
		threadId,
		onThreadNotFound: noop,
	}) as ReactElement<ThreadViewProps>;
}

// Regression: switching chats in a notebook reused one assistant-ui runtime and
// shrank its message list to empty, so a still-mounted message row threw
// "tapClientLookup: Index 0 out of bounds (length: 0)" into the error boundary.
test("each notebook chat mounts its own chat view, keyed by thread", () => {
	const first = pane(15);
	const second = pane(12);
	assert.equal(first.type, StubView);
	assert.equal(first.props.workspaceId, 1);
	assert.equal(first.props.chatId, 15);
	assert.equal(first.props.onThreadNotFound, noop);
	assert.equal(first.key, "15");
	assert.equal(second.key, "12");
	assert.notEqual(first.key, second.key, "a switch must remount, not reuse, the runtime");
	assert.equal(pane(15).key, first.key, "re-rendering the same chat keeps it mounted");
});
