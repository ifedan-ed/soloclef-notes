import assert from "node:assert/strict";
import test from "node:test";
import type { ThreadMessageLike } from "@assistant-ui/react";

import {
	type ElapsedAnchors,
	withLiveElapsed,
} from "@/features/chat-thread/server-turn/live-elapsed";

function savedTurn(activeDurationMs: number, status = "running"): ThreadMessageLike[] {
	return [
		{ id: "msg-122", role: "user", content: [{ type: "text", text: "search the web" }] },
		{
			id: "msg-123",
			role: "assistant",
			content: [
				{
					type: "data-activities",
					data: { activities: [], timing: { status, activeDurationMs } },
				},
				{ type: "text", text: "" },
			] as unknown as ThreadMessageLike["content"],
		},
	];
}

function projection(messages: ThreadMessageLike[]) {
	const part = (messages[1].content as unknown as Array<{ data?: Record<string, unknown> }>)[0];
	return part.data?.timingProjection;
}

test("a saved running time is anchored to when this page first saw it", () => {
	const anchors: ElapsedAnchors = new Map();
	const first = withLiveElapsed(savedTurn(30_000), anchors, 1_000);
	assert.deepEqual(projection(first), { baseDurationMs: 30_000, receivedAtPerformanceMs: 1_000 });

	// The same save seen again (a re-render) keeps counting from the first sighting.
	const again = withLiveElapsed(savedTurn(30_000), anchors, 9_000);
	assert.deepEqual(projection(again), { baseDurationMs: 30_000, receivedAtPerformanceMs: 1_000 });

	// A newer save moves the anchor.
	const newer = withLiveElapsed(savedTurn(42_000), anchors, 13_000);
	assert.deepEqual(projection(newer), { baseDurationMs: 42_000, receivedAtPerformanceMs: 13_000 });
});

test("finished turns and non-assistant tails are left alone", () => {
	const anchors: ElapsedAnchors = new Map();
	const done = savedTurn(250_000, "completed");
	assert.equal(withLiveElapsed(done, anchors, 1), done);

	const userLast = savedTurn(1_000).reverse();
	assert.equal(withLiveElapsed(userLast, anchors, 1), userLast);
});
