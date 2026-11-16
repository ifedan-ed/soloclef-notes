"use client";

import { BellRing, Sparkles } from "lucide-react";
import { useTranslations } from "next-intl";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { useStudioMutations, useStudioOutputs } from "../../hooks/use-notebook-queries";
import { useStudioNotifications } from "../../hooks/use-studio-notifications";
import { studioOpenAction } from "../../lib/open-output";
import { retryDecision } from "../../lib/studio-link";
import { newRequestId } from "../../lib/studio-request";
import { retryRequest } from "../../lib/studio-retry";
import type { NotebookSource, StudioKind, StudioOutput } from "../../model/notebook";
import { AudioOverviewView } from "./audio-overview-view";
import { GenerateDialog } from "./generate-dialog";
import { OutputView } from "./output-view";
import { StudioOutputRow } from "./studio-output-row";
import { StudioTiles } from "./studio-tiles";

export function StudioPanel({
	notebookId,
	notebookTitle,
	sources,
	sourcesLoaded,
	onAddSources,
	onResearch,
	openOutputId,
	onOpenOutput,
	retryOutputId,
	onRetryHandled,
}: {
	notebookId: number;
	notebookTitle: string;
	sources: NotebookSource[];
	/** The source list has been read: until then it is empty, not "none". */
	sourcesLoaded: boolean;
	onAddSources: () => void;
	onResearch: () => void;
	/** The output open in the output view (the notebook page widens the column for it). */
	openOutputId: number | null;
	onOpenOutput: (outputId: number | null) => void;
	/** A failed output a link asked to run again (the inbox's "Try again"), or null. */
	retryOutputId: number | null;
	/** Called once the link's output was run again, or found gone: the link is spent. */
	onRetryHandled: () => void;
}) {
	const t = useTranslations("notebooks");
	const outputs = useStudioOutputs(notebookId);
	const studio = useStudioMutations(notebookId);
	const [dialogKind, setDialogKind] = useState<StudioKind | null>(null);
	const [playing, setPlaying] = useState<StudioOutput | null>(null);
	const loading = outputs.isLoading;
	const showSkeleton = useDelayedFlag(loading);
	const notifications = useStudioNotifications(outputs.data);
	// Notes have a tab of their own beside this one (NotebookRightPanel); Studio
	// lists what was generated, newest first.
	const items = [...(outputs.data ?? [])].sort(
		(a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
	);
	const viewing = (outputs.data ?? []).find((output) => output.id === openOutputId) ?? null;

	// Run an output again with the options and sources it was made with
	// (retryRequest): the row's Retry and the inbox's "Try again" are the
	// same action. The new run takes the
	// failed one's place: the failed row (which holds no file) is removed
	// once the new one is queued, so the list does not keep a stale
	// "Failed" beside the output that replaced it.
	const retry = (output: StudioOutput) =>
		studio.generate.mutate(
			retryRequest(output, sourcesLoaded ? sources : undefined, newRequestId()),
			{
				onSuccess: (generated) => {
					notifications.markStarted(generated.id);
					studio.remove.mutate(output.id);
				},
				// The server's own sentence when it gave one (an account with
				// ten outputs already waiting is told so), as the dialog does.
				onError: (error) =>
					toast.error(
						error instanceof Error && error.message ? error.message : t("generate_failed")
					),
			}
		);

	// A link's retry runs once: the ref guards a double effect run, and the
	// page clears the id (onRetryHandled) so a remount does not run it again.
	const linkRetried = useRef<number | null>(null);
	useEffect(() => {
		if (retryOutputId === null || linkRetried.current === retryOutputId) return;
		const decision = retryDecision(outputs.data, retryOutputId);
		// The sources too: a retry sent before they are read could not leave
		// out the ones removed since.
		if (decision === "wait" || !sourcesLoaded) return;
		linkRetried.current = retryOutputId;
		const output = outputs.data?.find((candidate) => candidate.id === retryOutputId);
		if (decision === "retry" && output) retry(output);
		else toast.error(t("retry_link_gone"));
		onRetryHandled();
	});

	const openOutput = (output: StudioOutput) => {
		const action = studioOpenAction(output);
		if (action?.type === "audio-player") setPlaying(output);
		else if (action?.type === "studio-view") onOpenOutput(output.id);
	};

	if (playing?.artifact_id) {
		return (
			<AudioOverviewView
				notebookId={notebookId}
				output={playing}
				artifactId={playing.artifact_id}
				onBack={() => setPlaying(null)}
			/>
		);
	}

	return (
		<div className="relative flex min-h-0 flex-1 flex-col">
			{viewing?.artifact_id ? (
				<OutputView
					key={viewing.id}
					notebookId={notebookId}
					output={viewing}
					onClose={() => onOpenOutput(null)}
				/>
			) : null}
			{/* The list stays mounted under an open output (its scroll kept). */}
			<div
				className={cn(
					"flex min-h-0 flex-1 flex-col",
					viewing?.artifact_id ? "lg:hidden" : undefined
				)}
			>
				<div className="min-h-0 flex-1 overflow-y-auto px-3 pb-3">
					{notifications.offer ? (
						<div className="mb-3 space-y-2 rounded-xl border bg-muted/30 p-3" role="note">
							<p className="flex items-center gap-2 text-sm font-medium">
								<BellRing className="size-4 text-muted-foreground" aria-hidden />
								{t("notify.offer_title")}
							</p>
							<p className="text-xs text-muted-foreground">{t("notify.offer_body")}</p>
							<div className="flex gap-2">
								<Button size="sm" onClick={notifications.enable}>
									{t("notify.turn_on")}
								</Button>
								<Button size="sm" variant="ghost" onClick={notifications.dismiss}>
									{t("notify.not_now")}
								</Button>
							</div>
						</div>
					) : null}
					<StudioTiles onPick={setDialogKind} />
					<div className="my-4 border-t" />
					{loading ? (
						showSkeleton ? (
							<div className="space-y-2" aria-busy>
								{["a", "b", "c"].map((key) => (
									<Skeleton key={key} className="h-10 w-full" />
								))}
							</div>
						) : null
					) : items.length === 0 ? (
						<div className="flex flex-col items-center gap-2 px-4 py-8 text-center">
							<Sparkles className="size-6 text-muted-foreground" aria-hidden />
							<p className="text-sm font-medium">{t("studio_empty_title")}</p>
							<p className="text-xs text-muted-foreground">{t("studio_empty")}</p>
						</div>
					) : (
						<div className="space-y-0.5">
							{items.map((output) => (
								<StudioOutputRow
									key={`output-${output.id}`}
									output={output}
									onOpen={() => openOutput(output)}
									onRename={(title) => studio.rename.mutateAsync({ outputId: output.id, title })}
									onDelete={() => studio.remove.mutateAsync(output.id)}
									onRetry={() => retry(output)}
								/>
							))}
						</div>
					)}
				</div>
				<GenerateDialog
					kind={dialogKind}
					onOpenChange={(open) => {
						if (!open) setDialogKind(null);
					}}
					notebookId={notebookId}
					notebookTitle={notebookTitle}
					sources={sources}
					onGenerate={async (body) => {
						const output = await studio.generate.mutateAsync(body);
						notifications.markStarted(output.id);
						toast.success(t("generation_started"));
					}}
					onAddSources={onAddSources}
					onResearch={onResearch}
				/>
			</div>
		</div>
	);
}
