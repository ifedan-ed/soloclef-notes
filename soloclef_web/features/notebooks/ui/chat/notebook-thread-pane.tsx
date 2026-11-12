import type { ComponentType } from "react";

export interface ThreadViewProps {
	chatId: number;
	onThreadNotFound: () => void;
}

/**
 * The notebook's chat. Keyed by the thread so a new thread mounts a fresh chat
 * runtime: shrinking a live assistant-ui runtime's message list to empty (as
 * the thread swaps) lets still-mounted message rows read an index that no
 * longer exists, and assistant-ui throws "Index 0 out of bounds".
 */
export function NotebookThreadPane({
	view: View,
	threadId,
	onThreadNotFound,
}: {
	view: ComponentType<ThreadViewProps>;
	threadId: number;
	onThreadNotFound: () => void;
}) {
	return <View key={threadId} chatId={threadId} onThreadNotFound={onThreadNotFound} />;
}
