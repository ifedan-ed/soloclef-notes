import assert from "node:assert/strict";
import test from "node:test";

import { currentTraceStep } from "@/features/chat-messages/timeline/current-step";
import {
	buildActivityLookup,
	firstToolIndexByActivityId,
	type TracePartLike,
} from "@/features/chat-messages/timeline/grouping";
import { getActivityPresentation } from "@/features/chat-messages/timeline/presentation";

type Activity = {
	id: string;
	title: string;
	status: "running" | "completed";
	iconKey: string;
	category: "file" | "research" | "artifact" | "connector" | "action";
};

function activity({ id, title, status, iconKey, category }: Activity, sequence: number) {
	return {
		id,
		sequence,
		kind: id,
		status,
		title,
		category,
		iconKey,
		startedAt: "2026-10-10T10:00:00Z",
		...(status === "completed" ? { completedAt: "2026-10-10T10:00:01Z" } : {}),
	};
}

/** One answer's parts: its steps as tool calls, reasoning where asked, and the step journal. */
function answer(steps: (Activity | { reasoning: string; running: boolean })[]) {
	const parts: (TracePartLike & { running?: boolean })[] = [];
	const journal: ReturnType<typeof activity>[] = [];
	steps.forEach((step, index) => {
		if ("reasoning" in step) {
			parts.push({ type: "reasoning", text: step.reasoning, running: step.running });
			return;
		}
		parts.push({
			type: "tool-call",
			toolCallId: `call-${index}`,
			toolName: step.id,
			metadata: { activityId: step.id },
		});
		journal.push(activity(step, index + 1));
	});
	parts.push({ type: "data-activities", data: { activities: journal } });
	const indices = parts.flatMap((part, index) => (part.type === "data-activities" ? [] : [index]));
	return { parts, indices };
}

function lineOf(
	steps: Parameters<typeof answer>[0],
	{ threadRunning, showReasoning = true }: { threadRunning: boolean; showReasoning?: boolean }
) {
	const { parts, indices } = answer(steps);
	const activities = buildActivityLookup(parts).byId;
	const current = currentTraceStep({
		parts,
		indices,
		activities,
		firstActivityIndices: firstToolIndexByActivityId(parts),
		showReasoning,
		threadRunning,
		isPartRunning: (index) => parts[index].running === true,
	});
	if (current?.kind === "activity") {
		return {
			kind: "activity",
			title: getActivityPresentation(current.activity, threadRunning).title,
			iconKey: current.activity.iconKey,
		};
	}
	return current;
}

const accounts: Activity = {
	id: "accounts",
	title: "Listed email accounts",
	status: "completed",
	iconKey: "list-todo",
	category: "connector",
};
const searched = (n: number): Activity => ({
	id: `search-${n}`,
	title: "Searched email",
	status: "completed",
	iconKey: "search",
	category: "connector",
});

test("an email answer names only the step it is on: the ninth search, not a row per search", () => {
	const steps = [
		accounts,
		...Array.from({ length: 8 }, (_, n) => searched(n)),
		{
			id: "search-8",
			title: "Searching email",
			status: "running",
			iconKey: "search",
			category: "connector",
		} as Activity,
	];
	assert.deepEqual(lineOf(steps, { threadRunning: true }), {
		kind: "activity",
		title: "Searching email",
		iconKey: "search",
	});
});

test("each new step replaces the one before it", () => {
	const web: Activity = {
		id: "web",
		title: "Searching the web",
		status: "running",
		iconKey: "search",
		category: "research",
	};
	assert.deepEqual(lineOf([accounts], { threadRunning: true }), {
		kind: "activity",
		title: "Listed email accounts",
		iconKey: "list-todo",
	});
	assert.deepEqual(lineOf([accounts, web], { threadRunning: true }), {
		kind: "activity",
		title: "Searching the web",
		iconKey: "search",
	});
});

test("reasoning being written is the step the line names", () => {
	assert.deepEqual(
		lineOf([accounts, { reasoning: "Looking for mail from Protective", running: true }], {
			threadRunning: true,
		}),
		{ kind: "reasoning" }
	);
	assert.deepEqual(lineOf([{ reasoning: "Thinking", running: true }], { threadRunning: true }), {
		kind: "reasoning",
	});
});

test("once the reasoning is written, or the answer is over, the line is the latest tool step", () => {
	assert.deepEqual(
		lineOf([accounts, { reasoning: "Done thinking", running: false }], { threadRunning: true }),
		{ kind: "activity", title: "Listed email accounts", iconKey: "list-todo" }
	);
	assert.deepEqual(
		lineOf([accounts, { reasoning: "Cut off", running: true }], { threadRunning: false }),
		{ kind: "activity", title: "Listed email accounts", iconKey: "list-todo" }
	);
});

test("a finished answer keeps its last step, and an answer with only reasoning keeps its summary", () => {
	assert.deepEqual(lineOf([accounts, searched(0)], { threadRunning: false }), {
		kind: "activity",
		title: "Searched email",
		iconKey: "search",
	});
	assert.equal(
		lineOf([{ reasoning: "Thought it through", running: false }], { threadRunning: false }),
		null
	);
});

test("hidden reasoning (a public page) is never the line", () => {
	assert.deepEqual(
		lineOf([accounts, { reasoning: "Thinking", running: true }], {
			threadRunning: true,
			showReasoning: false,
		}),
		{ kind: "activity", title: "Listed email accounts", iconKey: "list-todo" }
	);
});
