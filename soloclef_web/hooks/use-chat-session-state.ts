"use client";

import { useQuery } from "@tanstack/react-query";
import { useSetAtom } from "jotai";
import { useEffect } from "react";
import { chatSessionStateAtom } from "@/atoms/chat/chat-session-state.atom";
import { fetchServerTurnStatus } from "@/features/chat-thread/server-turn/turn-status-api";
import { cacheKeys } from "@/lib/query-client/cache-keys";

/**
 * Whether the AI is answering on the viewed thread, from
 * GET /threads/{id}/turn-status. The change stream (`chats`) reports a turn
 * starting or ending on any tab or device, which reads it again here.
 * Call once per thread (in the thread view); it feeds the global atom.
 */
export function useChatSessionStateSync(threadId: number | null) {
	const setSessionState = useSetAtom(chatSessionStateAtom);

	const { data: status } = useQuery({
		queryKey: cacheKeys.threads.turnStatus(threadId ?? 0),
		queryFn: () => fetchServerTurnStatus(threadId as number),
		enabled: !!threadId,
		// A hint, not the page's content: the turn watcher reports real trouble.
		meta: { suppressGlobalErrorToast: true },
	});

	useEffect(() => {
		if (!threadId) {
			setSessionState(null);
			return;
		}
		setSessionState({ threadId, isAiResponding: !!status && status !== "idle" });
	}, [threadId, status, setSessionState]);
}
