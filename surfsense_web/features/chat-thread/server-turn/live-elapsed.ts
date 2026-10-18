import type { ThreadMessageLike } from "@assistant-ui/react";

/** When this page first saw each saved running time, per assistant message. */
export type ElapsedAnchors = Map<string, { baseDurationMs: number; seenAtMs: number }>;

interface ActivitiesPart {
	type: "data-activities";
	data: {
		timing?: { status?: unknown; activeDurationMs?: unknown };
		timingProjection?: unknown;
		[key: string]: unknown;
	};
}

function isRunningActivitiesPart(part: unknown): part is ActivitiesPart {
	if (typeof part !== "object" || part === null) return false;
	const candidate = part as { type?: unknown; data?: unknown };
	if (candidate.type !== "data-activities") return false;
	const timing = (candidate.data as ActivitiesPart["data"] | undefined)?.timing;
	return timing?.status === "running" && Number.isSafeInteger(timing.activeDurationMs);
}

/**
 * Keep the clock ticking on a turn that another page is running.
 *
 * The backend saves the running time with each progress save (every few
 * seconds at most, and only when a step changes), so a page reading the saved
 * row would show a clock that jumps or stands still during a long step. This
 * anchors the saved time to when this page first saw it, the same way the live
 * stream anchors its timing frames, so the elapsed time counts up smoothly.
 * Only the last assistant message is touched.
 */
export function withLiveElapsed(
	messages: readonly ThreadMessageLike[],
	anchors: ElapsedAnchors,
	nowMs: number
): ThreadMessageLike[] {
	const index = messages.findLastIndex((message) => message.role === "assistant");
	if (index < 0 || index !== messages.length - 1) return messages as ThreadMessageLike[];
	const message = messages[index];
	if (!Array.isArray(message.content) || !message.id) return messages as ThreadMessageLike[];

	const partIndex = message.content.findIndex(isRunningActivitiesPart);
	if (partIndex < 0) return messages as ThreadMessageLike[];
	const part = message.content[partIndex] as unknown as ActivitiesPart;
	const baseDurationMs = part.data.timing?.activeDurationMs as number;

	let anchor = anchors.get(message.id);
	if (!anchor || anchor.baseDurationMs !== baseDurationMs) {
		anchor = { baseDurationMs, seenAtMs: nowMs };
		anchors.set(message.id, anchor);
	}

	const content = [...message.content];
	content[partIndex] = {
		...part,
		data: {
			...part.data,
			timingProjection: {
				baseDurationMs,
				receivedAtPerformanceMs: anchor.seenAtMs,
			},
		},
	} as unknown as (typeof content)[number];
	const next = [...messages] as ThreadMessageLike[];
	next[index] = { ...message, content: content as ThreadMessageLike["content"] };
	return next;
}
