import type { StreamEvent } from "./chat-stream-stub";

/**
 * The turn the owner asked about on 2026-10-10 ("ok mail from protective?"):
 * reasoning, "Listed email accounts", then eight email searches and a short
 * answer, as the backend streams it (the golden streams' event shapes) and as
 * it saves it. Pushed through chat-stream-stub.ts.
 */

export const QUESTION = "ok mail from protective?";
export const SEARCHES = 8;
export const ANSWER = "Nothing from Protective in your mail in the last 30 days.";
const STARTED = "2026-10-10T09:00:00+00:00";
const DONE_AT = "2026-10-10T09:00:01+00:00";

type Step = { id: string; tool: string; running: string; done: string; iconKey: string };
export const ACCOUNTS: Step = {
	id: "act_turn_1",
	tool: "list_email_accounts",
	running: "Listing email accounts",
	done: "Listed email accounts",
	iconKey: "list-todo",
};
export const search = (n: number): Step => ({
	id: `act_turn_${n + 2}`,
	tool: "search_emails",
	running: "Searching email",
	done: "Searched email",
	iconKey: "search",
});
const SUBAGENT = { context: { subagentType: "email_imap" } };

function activity(step: Step, sequence: number, finished: boolean) {
	return {
		id: step.id,
		sequence,
		kind: step.tool,
		status: finished ? "completed" : "running",
		title: finished ? step.done : step.running,
		category: "connector",
		iconKey: step.iconKey,
		startedAt: STARTED,
		...(finished ? { completedAt: DONE_AT } : {}),
	};
}

const toolCall = (step: Step) => `call_${step.id}`;
const metadata = (step: Step) => ({ ...SUBAGENT, activityId: step.id });

export function stepStarts(step: Step, sequence: number): StreamEvent[] {
	return [
		{ type: "data-activity", data: activity(step, sequence, false) },
		{
			type: "tool-input-start",
			toolCallId: toolCall(step),
			toolName: step.tool,
			metadata: metadata(step),
		},
		{
			type: "tool-input-available",
			toolCallId: toolCall(step),
			toolName: step.tool,
			input: step.tool === "search_emails" ? { query: "from:protective" } : {},
			metadata: metadata(step),
		},
	];
}

export function stepEnds(step: Step, sequence: number): StreamEvent[] {
	return [
		{ type: "data-activity", data: activity(step, sequence, true) },
		{
			type: "tool-output-available",
			toolCallId: toolCall(step),
			output: { status: "completed" },
			metadata: metadata(step),
		},
	];
}

export const HEAD: StreamEvent[] = [
	{ type: "start", messageId: "msg_chat_steps_line" },
	{ type: "start-step" },
	{ type: "data-turn-status", data: { status: "busy" } },
	{ type: "data-activity-timing", data: { status: "running", activeDurationMs: 120 } },
	{ type: "reasoning-start", id: "rs_1", startedAt: STARTED },
	{ type: "reasoning-delta", id: "rs_1", delta: "Looking through the mail for Protective." },
];

export const TASK: StreamEvent[] = [
	{ type: "reasoning-end", id: "rs_1", completedAt: DONE_AT },
	{
		type: "tool-input-available",
		toolCallId: "call_task_email",
		toolName: "task",
		input: { subagent_type: "email_imap", description: "Find mail from Protective" },
		metadata: SUBAGENT,
	},
];

export const TAIL: StreamEvent[] = [
	{ type: "tool-output-available", toolCallId: "call_task_email", output: { status: "completed" } },
	{ type: "text-start", id: "text_1" },
	{ type: "text-delta", id: "text_1", delta: ANSWER },
	{ type: "text-end", id: "text_1" },
	{ type: "data-activity-timing", data: { status: "completed", activeDurationMs: 79_000 } },
	{ type: "finish-step" },
	{ type: "finish" },
	"[DONE]",
];

/** The turn as the backend saves it: the step journal, the reasoning, the tool calls, the text. */
export function savedRows(threadId: number) {
	const steps = [ACCOUNTS, ...Array.from({ length: SEARCHES }, (_, n) => search(n))];
	const toolPart = (step: Step) => ({
		type: "tool-call",
		toolCallId: toolCall(step),
		toolName: step.tool,
		args: {},
		argsText: "{}",
		result: { status: "completed" },
		metadata: metadata(step),
	});
	return [
		{
			id: 900_001,
			thread_id: threadId,
			role: "user",
			content: [{ type: "text", text: QUESTION }],
			created_at: STARTED,
			author_id: null,
			turn_id: "chat-steps-line",
		},
		{
			id: 900_002,
			thread_id: threadId,
			role: "assistant",
			content: [
				{
					type: "data-activities",
					data: {
						timing: { status: "completed", activeDurationMs: 79_000 },
						activities: steps.map((step, i) => activity(step, i + 1, true)),
					},
				},
				{ type: "reasoning", text: "Looking through the mail for Protective." },
				{
					type: "tool-call",
					toolCallId: "call_task_email",
					toolName: "task",
					args: { subagent_type: "email_imap" },
					result: { status: "completed" },
					metadata: SUBAGENT,
				},
				...steps.map(toolPart),
				{ type: "text", text: ANSWER },
			],
			created_at: DONE_AT,
			author_id: null,
			turn_id: "chat-steps-line",
		},
	];
}
