// Marks an assistant message whose text is the live stream's, not the
// saved row's.
//
// The difference matters for citations: a streamed answer cites with the
// model's "[3]" labels, and the saved answer that replaces it has
// "[citation:<ref>]" markers instead (soloclef_assistant/cite). The chat
// draws the labels as chips only on a message marked here, or on the last
// one while the thread runs (components/assistant-ui/assistant-message.tsx).
// The mark covers the moment between the stream's end and the saved answer
// arriving, when the thread is no longer running but the text on screen is
// still the streamed one: without it the chips would turn back into
// "[3]" for that moment.

import type { ThreadMessageLike } from "@assistant-ui/react";

export const STREAMED_MESSAGE_METADATA = {
	custom: { streamedText: true },
} as const satisfies ThreadMessageLike["metadata"];

/** Whether a message's metadata carries the mark. */
export function isStreamedMessage(metadata: unknown): boolean {
	const custom = (metadata as { custom?: { streamedText?: unknown } } | undefined)?.custom;
	return custom?.streamedText === true;
}
