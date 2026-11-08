import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, isAbortError, NetworkError } from "../api/errors";
import type { NotebookThread } from "../api/schemas";
import { streamingFetch, useApp } from "../app-context";
import { keys } from "../queries";
import { buildChatRequest, type MentionedSource } from "./request";
import { streamChatTurn } from "./send";
import { type ContentPart, mentionsFromStored, partsFromStored, StreamError } from "./stream-state";

export interface ChatMessage {
	key: string;
	role: "user" | "assistant";
	parts: ContentPart[];
	mentions?: string[];
	status: "done" | "streaming" | "error";
	error?: string;
	/** The stream's error code (billing refusals link to the billing page). */
	errorCode?: string;
	truncated?: boolean;
	/** The persisted row id, once the server reports it. */
	dbId?: number;
}

/**
 * One notebook, one conversation (the web dropped multi-chat): open the most
 * recent thread, or create the first. Sends stream through ``/api/v1/new_chat``
 * and render as they arrive; the persisted history is reloaded afterwards.
 */
export function useNotebookChat(workspaceId: number, notebookId: number) {
	const { ep, api, t } = useApp();
	const queryClient = useQueryClient();
	const threads = useQuery({
		queryKey: keys.threads(workspaceId, notebookId),
		queryFn: () => ep.threads(workspaceId, notebookId),
		enabled: workspaceId > 0 && notebookId > 0,
	});
	const [createdThread, setCreatedThread] = useState<NotebookThread | null>(null);
	const [threadError, setThreadError] = useState(false);
	const creating = useRef(false);
	const thread = threads.data?.[0] ?? createdThread;
	const threadId = thread?.id ?? 0;

	useEffect(() => {
		if (!threads.data || threads.data.length > 0 || createdThread || creating.current) return;
		creating.current = true;
		ep.createThread(workspaceId, notebookId)
			.then((created) => {
				setCreatedThread(created);
				queryClient.setQueryData(keys.threads(workspaceId, notebookId), [created]);
			})
			.catch(() => setThreadError(true))
			.finally(() => {
				creating.current = false;
			});
	}, [threads.data, createdThread, ep, workspaceId, notebookId, queryClient]);

	const history = useQuery({
		queryKey: keys.history(threadId),
		queryFn: () => ep.threadMessages(threadId),
		enabled: threadId > 0,
	});

	const persisted = useMemo<ChatMessage[]>(
		() =>
			(history.data?.messages ?? [])
				.filter((m) => m.role === "user" || m.role === "assistant")
				.map((m) => ({
					key: `db-${m.id}`,
					dbId: m.id,
					role: m.role as "user" | "assistant",
					parts: partsFromStored(m.content),
					mentions: m.role === "user" ? mentionsFromStored(m.content) : undefined,
					status: "done" as const,
				}))
				.filter((m) => m.parts.length > 0 || (m.mentions?.length ?? 0) > 0),
		[history.data]
	);

	// While a turn runs (and until the reloaded history catches up) the local
	// copy is what the screen shows, so nothing flashes back to the old list.
	const [local, setLocal] = useState<ChatMessage[] | null>(null);
	const [running, setRunning] = useState(false);
	const [sendError, setSendError] = useState<{
		message: string;
		draft: string;
		mentions: MentionedSource[];
		/** The refusal's code and HTTP status, when the server answered. */
		code?: string;
		status?: number;
	} | null>(null);
	const controller = useRef<AbortController | null>(null);
	// Once the reloaded history holds everything shown locally, hand back to it.
	const handedBack = !!local && !running && !local.some((m) => m.status !== "done") && persisted.length >= local.length;
	const messages = local && !handedBack ? local : persisted;

	useEffect(() => () => controller.current?.abort(), []);

	const patchAssistant = (key: string, patch: Partial<ChatMessage>) =>
		setLocal((current) => (current ?? []).map((m) => (m.key === key ? { ...m, ...patch } : m)));

	const send = useCallback(
		async (query: string, mentions: MentionedSource[] = []) => {
			const text = query.trim();
			if (!text || running || threadId <= 0) return;
			setSendError(null);
			const prior = messages;
			const stamp = Date.now();
			const userMessage: ChatMessage = {
				key: `local-user-${stamp}`,
				role: "user",
				parts: [{ type: "text", text }],
				mentions: mentions.map((m) => m.title),
				status: "done",
			};
			const assistantKey = `local-assistant-${stamp}`;
			setLocal([...prior, userMessage, { key: assistantKey, role: "assistant", parts: [], status: "streaming" }]);
			setRunning(true);
			const abort = new AbortController();
			controller.current = abort;
			let accepted = false;
			try {
				const parts = await streamChatTurn({
					api,
					fetchImpl: streamingFetch,
					signal: abort.signal,
					body: buildChatRequest({
						threadId,
						workspaceId,
						query: text,
						history: prior.map((m) => ({ role: m.role, parts: m.parts })),
						mentions,
					}),
					onAccepted: () => {
						accepted = true;
					},
					effects: {
						onAssistantMessageId: (id) => patchAssistant(assistantKey, { dbId: id }),
						onTruncated: () => patchAssistant(assistantKey, { truncated: true }),
					},
					onUpdate: (next) => patchAssistant(assistantKey, { parts: next }),
				});
				patchAssistant(assistantKey, { parts, status: "done" });
			} catch (error) {
				if (isAbortError(error)) {
					patchAssistant(assistantKey, { status: "done" });
				} else if (!accepted) {
					// Never reached the server's turn: take the optimistic pair back and
					// give the composer its text again, as the web does.
					setLocal(prior.length > 0 || persisted.length > 0 ? prior : null);
					const message =
						error instanceof NetworkError
							? t("mobile.offline")
							: error instanceof ApiError && error.message
								? error.message
								: t("mobile.answer_failed");
					setSendError({
						message,
						draft: text,
						mentions,
						code: error instanceof ApiError ? error.code : undefined,
						status: error instanceof ApiError ? error.status : undefined,
					});
				} else {
					patchAssistant(assistantKey, {
						status: "error",
						error: error instanceof StreamError ? error.message : t("mobile.answer_failed"),
						errorCode: error instanceof StreamError ? error.errorCode : undefined,
					});
				}
			} finally {
				controller.current = null;
				setRunning(false);
				if (threadId > 0) {
					void queryClient.invalidateQueries({ queryKey: keys.history(threadId) });
				}
			}
		},
		[running, threadId, workspaceId, api, t, queryClient, persisted.length, messages]
	);

	const stop = useCallback(async () => {
		controller.current?.abort();
		if (threadId > 0) {
			try {
				await ep.cancelTurn(threadId);
			} catch {
				// the stream is already closed on our side
			}
		}
	}, [ep, threadId]);

	return {
		threadId,
		messages,
		running,
		send,
		stop,
		sendError,
		clearSendError: () => setSendError(null),
		loading: threads.isLoading || (threadId > 0 && history.isLoading),
		failed: threadError || threads.isError || history.isError,
		retryLoad: () => {
			setThreadError(false);
			void threads.refetch();
			void history.refetch();
		},
	};
}
