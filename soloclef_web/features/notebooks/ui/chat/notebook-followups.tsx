"use client";

import { useAuiState, useThreadRuntime } from "@assistant-ui/react";
import { useQuery } from "@tanstack/react-query";
import { CornerDownRight } from "lucide-react";
import { useTranslations } from "next-intl";
import { useEffect } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { notebookKeys } from "../../api/notebook-query-keys";
import { notebooksApi } from "../../api/notebooks-api";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { answerTextOf } from "../../lib/answer-text";
import { useNotebookChat } from "../../state/notebook-chat-context";

/** Notebooks whose chat has a turn running (or just finished) on this page. */
const turnsRunning = new Set<number>();

/**
 * Three suggested next questions under the latest answer of a notebook chat,
 * generated once per answer from the answer and the notebook's sources.
 */
export function NotebookFollowups() {
	const notebook = useNotebookChat();
	const t = useTranslations("notebooks");
	const thread = useThreadRuntime();
	const isLast = useAuiState(({ message }) => message?.isLast ?? false);
	const running = useAuiState(({ thread: state }) => state.isRunning);
	const messageId = useAuiState(({ message }) => message?.id ?? "");
	const content = useAuiState(({ message }) => message?.content);
	const answer = answerTextOf(content);
	const enabled = !!notebook && isLast && !running && answer.trim().length > 0;
	// Wait for the finished answer to settle (it is re-keyed once persisted).
	const settled = useDelayedFlag(enabled, 1200);

	const followups = useQuery({
		queryKey: notebookKeys.followups(
			notebook?.workspaceId ?? 0,
			notebook?.notebookId ?? 0,
			messageId
		),
		queryFn: () =>
			notebooksApi.followups(notebook?.workspaceId ?? 0, notebook?.notebookId ?? 0, answer),
		enabled: settled,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	const showSkeleton = useDelayedFlag(settled && followups.isLoading, 400);

	// A turn just ended: an unnamed notebook may have named itself from the
	// question. Kept per notebook, not per message, because the finished
	// answer is re-keyed (remounted) once persisted.
	const notebookId = notebook?.notebookId ?? 0;
	if (running && isLast && notebookId) turnsRunning.add(notebookId);
	const watchTitle = notebook?.watchTitle;
	useEffect(() => {
		if (!settled || !turnsRunning.delete(notebookId)) return;
		watchTitle?.();
	}, [settled, notebookId, watchTitle]);

	if (!enabled || !settled) return null;

	const ask = (question: string) => {
		thread.composer.setText(question);
		thread.composer.send();
	};

	if (followups.isLoading) {
		return showSkeleton ? (
			<div className="ml-2 mb-6 grid gap-2 sm:grid-cols-3" aria-busy>
				{["a", "b", "c"].map((key) => (
					<Skeleton key={key} className="h-14 rounded-xl" />
				))}
			</div>
		) : null;
	}
	const suggestions = followups.data?.suggestions ?? [];
	if (suggestions.length === 0) return null;
	return (
		<nav aria-label={t("followups_label")} className="ml-2 mb-6 grid gap-2 sm:grid-cols-3">
			{suggestions.map((question) => (
				<button
					key={question}
					type="button"
					onClick={() => ask(question)}
					className="flex items-start gap-2 rounded-xl border bg-muted/30 p-3 text-left text-sm transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					<CornerDownRight className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
					<span className="line-clamp-3">{question}</span>
				</button>
			))}
		</nav>
	);
}
