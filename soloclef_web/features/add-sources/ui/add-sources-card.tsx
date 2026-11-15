"use client";

import type { ToolCallMessagePartProps } from "@assistant-ui/react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertCircleIcon, BookPlusIcon, CheckCircle2Icon, Loader2Icon } from "lucide-react";
import { useEffect } from "react";
import { notebookKeys } from "@/features/notebooks/api/notebook-query-keys";
import { useNotebookSources } from "@/features/notebooks/hooks/use-notebook-queries";
import { useNotebookChat } from "@/features/notebooks/state/notebook-chat-context";
import {
	type AddSourcesCardData,
	type AddSourcesItem,
	addSourcesSummary,
	itemLabel,
	parseAddSourcesCard,
	withLiveReading,
} from "../add-sources-card";

function StateIcon({ entry }: { entry: AddSourcesItem }) {
	if (entry.status === "not_added") {
		return <AlertCircleIcon className="size-4 shrink-0 text-destructive" aria-label="Not added" />;
	}
	if (entry.reading) {
		return (
			<Loader2Icon
				className="size-4 shrink-0 animate-spin text-muted-foreground"
				aria-label="Reading"
			/>
		);
	}
	return <CheckCircle2Icon className="size-4 shrink-0 text-emerald-600" aria-label="Added" />;
}

/** The short word beside a row: labels only, as every card here. */
function stateWord(entry: AddSourcesItem): string | null {
	if (entry.status === "not_added") return null;
	if (entry.reading) return "Reading";
	return entry.status === "already_a_source" ? "Already a source" : null;
}

function Row({ entry, onOpen }: { entry: AddSourcesItem; onOpen?: (documentId: number) => void }) {
	const label = itemLabel(entry);
	const word = stateWord(entry);
	const documentId = entry.status !== "not_added" ? entry.document_id : undefined;
	const body = (
		<>
			<StateIcon entry={entry} />
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm" title={label}>
					{label}
				</span>
				{entry.status === "not_added" && entry.reason ? (
					<span className="block break-words text-xs text-muted-foreground">{entry.reason}</span>
				) : null}
			</span>
			{word ? <span className="shrink-0 text-xs text-muted-foreground">{word}</span> : null}
		</>
	);
	// A source opens in the notebook's Sources panel; outside a notebook (a
	// shared copy of the thread) the row is the title alone.
	if (documentId && onOpen) {
		return (
			<li>
				<button
					type="button"
					onClick={() => onOpen(documentId)}
					className="flex w-full items-start gap-2 px-4 py-2.5 text-left transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:outline-none"
				>
					{body}
				</button>
			</li>
		);
	}
	return <li className="flex items-start gap-2 px-4 py-2.5">{body}</li>;
}

/** The sources one add_sources call added to the notebook: each opens there. */
export function AddSourcesCard({
	data,
	onOpen,
}: {
	data: AddSourcesCardData;
	onOpen?: (documentId: number) => void;
}) {
	return (
		<div className="my-4 w-full max-w-lg overflow-hidden rounded-xl border bg-card">
			<div className="flex items-center gap-2 px-4 py-2.5">
				<BookPlusIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
				<span className="min-w-0 flex-1 truncate text-sm">{addSourcesSummary(data)}</span>
			</div>
			<ul className="divide-y border-t">
				{data.items.map((entry, index) => (
					// One identity may be sent twice; the list is fixed once the call has ended.
					// biome-ignore lint/suspicious/noArrayIndexKey: see above
					<Row key={`${index}:${entry.document_id ?? entry.ref}`} entry={entry} onOpen={onOpen} />
				))}
			</ul>
		</div>
	);
}

/**
 * add_sources in an answer. It draws only a finished call whose result is
 * the card payload (isBodyTool); anything else stays in the turn's trace.
 */
export function AddSourcesToolUI({
	result,
}: ToolCallMessagePartProps<{ sources?: string[] }, unknown>) {
	const data = parseAddSourcesCard(result);
	const notebook = useNotebookChat();
	const queryClient = useQueryClient();
	// The chat added rows to the notebook's sources behind the Sources
	// panel's back: it is told to read them again, once per card, and then
	// follows their reading itself (it polls while one is pending).
	const notebookId = data?.notebook_id;
	useEffect(() => {
		if (!notebookId) return;
		void queryClient.invalidateQueries({ queryKey: notebookKeys.sources(notebookId) });
	}, [notebookId, queryClient]);
	// Inside the notebook the sources were added to, "Reading" follows the
	// sources as they are now (that list polls while one is pending), not
	// as they were when the answer was saved, which would read for ever.
	const here = !!data && notebook?.notebookId === data.notebook_id;
	const sources = useNotebookSources(here && data ? data.notebook_id : 0);
	if (!data) return null;
	// Only the notebook the sources were added to can open them.
	const onOpen = here ? notebook?.openSource : undefined;
	return <AddSourcesCard data={withLiveReading(data, sources.data)} onOpen={onOpen} />;
}
