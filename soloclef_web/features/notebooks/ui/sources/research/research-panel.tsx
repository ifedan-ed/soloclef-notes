"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useDelayedFlag } from "../../../hooks/use-delayed-flag";
import { useReadWebPage } from "../../../hooks/use-read-web-page";
import { useRememberedSourcePicks } from "../../../hooks/use-remembered-source-picks";
import { useResearchImport } from "../../../hooks/use-research-import";
import { useResearchSearch } from "../../../hooks/use-research-search";
import {
	usePageReader,
	useResearchSourcesStatus,
} from "../../../hooks/use-research-sources-status";
import { researchRequestFor, withoutUnavailable } from "../../../lib/research-source-picks";
import type { ResearchRequest } from "../../../model/research";
import { isResearchSourceId, READ_PAGE_ID } from "../../../model/research-sources";
import { type ReadPageState, ReadPageStatus } from "./read-page-status";
import { ResearchError } from "./research-error";
import { initialResearchDraft, type ResearchDraft, ResearchForm } from "./research-form";
import { ResearchProgress } from "./research-progress";
import { ResearchResults } from "./research-results";

/**
 * Search the web for new sources and add the picks to the notebook, or read
 * one pasted page ("Read a web page") and add it.
 * ``focusRequest`` is a token: each new non-zero value brings the box into
 * view and puts the cursor in the query, then ``onFocusHandled`` clears it.
 */
export function ResearchPanel({
	workspaceId,
	notebookId,
	focusRequest,
	onFocusHandled,
}: {
	workspaceId: number;
	notebookId: number;
	focusRequest: number;
	onFocusHandled: () => void;
}) {
	const { state, search, clear } = useResearchSearch(workspaceId, notebookId);
	const importer = useResearchImport(workspaceId, notebookId);
	const unavailable = useResearchSourcesStatus(workspaceId, notebookId);
	const pageReader = usePageReader(workspaceId, notebookId);
	const pageAdder = useReadWebPage(workspaceId, notebookId);
	const [pageState, setPageState] = useState<ReadPageState>({ status: "idle" });
	const [text, setText] = useState({
		query: initialResearchDraft.query,
		mode: initialResearchDraft.mode,
	});
	const [rememberedPicks, rememberPicks] = useRememberedSourcePicks();
	const picks = useMemo(
		() => withoutUnavailable(rememberedPicks, unavailable),
		[rememberedPicks, unavailable]
	);
	const draft: ResearchDraft = { ...text, picks };
	const setDraft = (next: ResearchDraft) => {
		setText({ query: next.query, mode: next.mode });
		if (next.picks !== picks) rememberPicks(next.picks);
	};
	const containerRef = useRef<HTMLDivElement>(null);
	const inputRef = useRef<HTMLInputElement>(null);
	const showProgress = useDelayedFlag(state.status === "searching");

	useEffect(() => {
		if (!focusRequest) return;
		// Wait a frame: the panel may have just been expanded or switched to.
		const frame = window.requestAnimationFrame(() => {
			containerRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
			inputRef.current?.focus({ preventScroll: true });
			onFocusHandled();
		});
		return () => window.cancelAnimationFrame(frame);
	}, [focusRequest, onFocusHandled]);

	const addPage = (url: string) => {
		setPageState({ status: "adding" });
		pageAdder.mutate(url, {
			onSuccess: () => {
				setPageState({ status: "added" });
				setText((current) => ({ ...current, query: "" }));
			},
			onError: (error) => setPageState({ status: "failed", message: error.message || null }),
		});
	};

	const start = () => {
		const query = draft.query.trim();
		setPageState({ status: "idle" });
		if (picks.source === READ_PAGE_ID) {
			clear();
			addPage(query);
			return;
		}
		const request = researchRequestFor(query, picks, draft.mode);
		if (request) {
			importer.reset();
			void search(request);
		}
	};

	const retrySearch = (request: ResearchRequest) => {
		importer.reset();
		void search(request);
	};

	const dismiss = () => {
		importer.reset();
		clear();
	};

	return (
		<div ref={containerRef} className="scroll-mt-2 space-y-2">
			<ResearchForm
				draft={draft}
				onDraftChange={setDraft}
				onSubmit={start}
				inputRef={inputRef}
				unavailable={unavailable}
				pageReader={pageReader}
				busy={pageState.status === "adding"}
			/>
			<ReadPageStatus
				state={pageState}
				pageReader={pageReader}
				onDismiss={() => setPageState({ status: "idle" })}
			/>
			{state.status === "searching" && showProgress ? (
				<ResearchProgress
					phase={state.phase}
					progress={state.progress}
					providers={state.providers}
					onCancel={dismiss}
				/>
			) : null}
			{state.status === "error" ? (
				<ResearchError
					message={state.message}
					code={state.code}
					source={isResearchSourceId(state.source) ? state.source : state.request.source}
					onRetry={() => retrySearch(state.request)}
					onDismiss={dismiss}
				/>
			) : null}
			{state.status === "results" ? (
				<ResearchResults
					results={state.results}
					source={state.request.source}
					providers={state.providers}
					several={(state.request.sources?.length ?? 0) > 1}
					importing={importer.isPending}
					importFailed={importer.isError}
					onImport={(items) => importer.mutate(items, { onSuccess: dismiss })}
					onDismiss={dismiss}
				/>
			) : null}
		</div>
	);
}
