import type { ThreadMessageLike } from "@assistant-ui/react";

/** The saved row id behind a page message id (`msg-<id>`); null while it isn't saved yet. */
export function savedMessageId(messageId: string | null | undefined): number | null {
	const match = messageId?.match(/^msg-(\d+)$/);
	return match ? Number.parseInt(match[1], 10) : null;
}

/**
 * The question a Regenerate asks again: the user message at or before
 * `fromId` (assistant-ui passes the answer's parent), else the latest one.
 */
export function regeneratedQuestion(
	messages: readonly ThreadMessageLike[],
	fromId?: string | null
): ThreadMessageLike | undefined {
	const fromIndex = fromId ? messages.findIndex((m) => m.id === fromId) : -1;
	const end = fromIndex >= 0 ? fromIndex : messages.length - 1;
	for (let i = end; i >= 0; i--) {
		if (messages[i].role === "user") return messages[i];
	}
	return undefined;
}
