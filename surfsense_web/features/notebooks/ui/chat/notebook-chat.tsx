"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { ChatThreadView } from "@/features/chat-thread/chat-thread-view";
import { notebookKeys } from "../../api/notebook-query-keys";
import { notebooksApi } from "../../api/notebooks-api";
import { useNotebookThreads } from "../../hooks/use-notebook-queries";
import { hasRetiredChatParam, withoutRetiredChatParam } from "../../lib/retired-chat-param";
import { NotebookThreadPane } from "./notebook-thread-pane";

/**
 * A notebook has one running conversation, as in NotebookLM: Studio outputs
 * belong to the notebook, not to a chat, so separate chats added nothing.
 * The latest chat opens (a notebook without one gets its first); the URL never
 * names it, and "Delete chat history" in the notebook menu is the fresh start.
 */
export function NotebookChat({
	workspaceId,
	notebookId,
}: {
	workspaceId: number;
	notebookId: number;
}) {
	const t = useTranslations("notebooks");
	const queryClient = useQueryClient();
	const threads = useNotebookThreads(workspaceId, notebookId);
	const [threadId, setThreadId] = useState<number | null>(null);
	const creatingRef = useRef(false);

	// Old links named a chat (?chat=); drop it without a navigation. Deferred:
	// the router writes the URL after effects run.
	useEffect(() => {
		const timer = window.setTimeout(() => {
			if (!hasRetiredChatParam(window.location.search)) return;
			window.history.replaceState(
				window.history.state,
				"",
				withoutRetiredChatParam(window.location.href)
			);
		}, 0);
		return () => window.clearTimeout(timer);
	}, []);

	const startChat = async () => {
		if (creatingRef.current) return;
		creatingRef.current = true;
		try {
			const thread = await notebooksApi.createThread(workspaceId, notebookId);
			queryClient.setQueryData(notebookKeys.threads(workspaceId, notebookId), (current = []) => [
				thread,
				...(current as (typeof thread)[]),
			]);
			setThreadId(thread.id);
		} catch {
			toast.error(t("new_chat_failed"));
		} finally {
			creatingRef.current = false;
		}
	};

	// Show the most recent chat; a notebook without one (new, or history
	// deleted) gets its first.
	// biome-ignore lint/correctness/useExhaustiveDependencies: runs when the list changes
	useEffect(() => {
		if (!threads.data) return;
		if (threadId && threads.data.some((thread) => thread.id === threadId)) return;
		if (threads.data.length > 0) {
			setThreadId(threads.data[0].id);
		} else {
			void startChat();
		}
	}, [threads.data]);

	return (
		<div className="flex min-h-0 min-w-0 flex-1 flex-col">
			<div className="min-h-0 flex-1">
				{threadId ? (
					<NotebookThreadPane
						view={ChatThreadView}
						workspaceId={workspaceId}
						threadId={threadId}
						onThreadNotFound={() => {
							setThreadId(null);
							void threads.refetch();
						}}
					/>
				) : null}
			</div>
		</div>
	);
}
