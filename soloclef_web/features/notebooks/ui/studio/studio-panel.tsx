"use client";

import { ArrowLeft, BellRing, Sparkles } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { PodcastPlayer } from "@/components/tool-ui/podcast/player";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { useStudioMutations, useStudioOutputs } from "../../hooks/use-notebook-queries";
import { useStudioNotifications } from "../../hooks/use-studio-notifications";
import { studioOpenAction } from "../../lib/open-output";
import { newRequestId } from "../../lib/studio-request";
import type { NotebookSource, StudioKind, StudioOutput } from "../../model/notebook";
import { GenerateDialog } from "./generate-dialog";
import { OutputView } from "./output-view";
import { StudioOutputRow } from "./studio-output-row";
import { StudioTiles } from "./studio-tiles";

export function StudioPanel({
	notebookId,
	notebookTitle,
	sources,
	onAddSources,
	onResearch,
	openOutputId,
	onOpenOutput,
}: {
	notebookId: number;
	notebookTitle: string;
	sources: NotebookSource[];
	onAddSources: () => void;
	onResearch: () => void;
	/** The output open in the output view (the notebook page widens the column for it). */
	openOutputId: number | null;
	onOpenOutput: (outputId: number | null) => void;
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

	const openOutput = (output: StudioOutput) => {
		const action = studioOpenAction(output);
		if (action?.type === "audio-player") setPlaying(output);
		else if (action?.type === "studio-view") onOpenOutput(output.id);
	};

	if (playing?.artifact_id) {
		return (
			<div className="flex min-h-0 flex-1 flex-col">
				<div className="flex items-center gap-2 px-3 pb-2">
					<Button
						variant="ghost"
						size="icon"
						className="size-8 rounded-full"
						onClick={() => setPlaying(null)}
						aria-label={t("back_to_studio")}
					>
						<ArrowLeft className="size-4" />
					</Button>
					<h3 className="min-w-0 flex-1 truncate text-sm font-medium">{playing.title}</h3>
				</div>
				<div className="min-h-0 flex-1 overflow-y-auto px-3">
					<PodcastPlayer
						artifactId={playing.artifact_id}
						title={playing.title}
						durationMs={playing.duration_seconds ? playing.duration_seconds * 1000 : undefined}
					/>
				</div>
			</div>
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
									onRetry={() =>
										studio.generate.mutate(
											{
												...(output.options as Record<string, unknown>),
												kind: output.kind,
												title: output.title,
												request_id: newRequestId(),
											},
											{
												onSuccess: (generated) => notifications.markStarted(generated.id),
												onError: () => toast.error(t("generate_failed")),
											}
										)
									}
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
