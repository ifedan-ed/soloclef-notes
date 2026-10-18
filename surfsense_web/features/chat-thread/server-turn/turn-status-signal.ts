type Listener = (threadId: number) => void;

const listeners = new Set<Listener>();

/**
 * Something just changed a thread's turn on the server, so the page showing
 * that thread should ask the backend again rather than wait for its next
 * check: a send, Regenerate or approval was refused because a turn is running
 * (409 THREAD_BUSY / TURN_CANCELLING), or a Stop request was answered.
 */
export function askForTurnStatusCheck(threadId: number): void {
	for (const listener of listeners) listener(threadId);
}

export function onTurnStatusCheckRequested(listener: Listener): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}
