"use client";

import { ArrowLeft, BellRing, NotebookPen, Sparkles } from "lucide-react";
import { useTranslations } from "next-intl";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { preloadPlateEditor } from "@/components/editor/plate-editor-module";
import { PodcastPlayer } from "@/components/tool-ui/podcast/player";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import {
	useNotebookNotes,
	useNoteMutations,
	useStudioMutations,
	useStudioOutputs,
} from "../../hooks/use-notebook-queries";
import { useStudioNotifications } from "../../hooks/use-studio-notifications";
import { studioOpenAction } from "../../lib/open-output";
import { newRequestId } from "../../lib/studio-request";
import type { NotebookNote, NotebookSource, StudioKind, StudioOutput } from "../../model/notebook";
import { GenerateDialog } from "./generate-dialog";
import { NoteEditor } from "./note-editor";
import { NoteRow } from "./note-row";
import { OutputView } from "./output-view";
import { StudioOutputRow } from "./studio-output-row";
import { StudioTiles } from "./studio-tiles";

type Item =
	| { type: "output"; at: string; output: StudioOutput }
	| { type: "note"; at: string; note: NotebookNote };

export function StudioPanel({
	workspaceId,
	notebookId,
	notebookTitle,
	sources,
	onAddSources,
	onResearch,
	initialOpenNoteId = null,
	openOutputId,
	onOpenOutput,
}: {
	workspaceId: number;
	notebookId: number;
	notebookTitle: string;
	sources: NotebookSource[];
	onAddSources: () => void;
	onResearch: () => void;
	/** A note that opens straight away (a home search result's). */
	initialOpenNoteId?: number | null;
	/** The output open in the output view (the workspace widens the column for it). */
	openOutputId: number | null;
	onOpenOutput: (outputId: number | null) => void;
}) {
	const t = useTranslations("notebooks");
	const outputs = useStudioOutputs(workspaceId, notebookId);
	const notes = useNotebookNotes(workspaceId, notebookId);
	const studio = useStudioMutations(workspaceId, notebookId);
	const noteMutations = useNoteMutations(workspaceId, notebookId);
	const [dialogKind, setDialogKind] = useState<StudioKind | null>(null);
	const [openNoteId, setOpenNoteId] = useState<number | null>(initialOpenNoteId);
	const [playing, setPlaying] = useState<StudioOutput | null>(null);
	const loading = outputs.isLoading || notes.isLoading;
	const showSkeleton = useDelayedFlag(loading);
	const notifications = useStudioNotifications(outputs.data);
	// A note then opens drawn whole, not after a placeholder (plate-editor-module.ts).
	const hasNotes = (notes.data?.length ?? 0) > 0 || initialOpenNoteId !== null;
	useEffect(() => {
		if (hasNotes) preloadPlateEditor().catch(() => undefined);
	}, [hasNotes]);

	const items: Item[] = [
		...(outputs.data ?? []).map((output) => ({
			type: "output" as const,
			at: output.created_at,
			output,
		})),
		...(notes.data ?? []).map((note) => ({
			type: "note" as const,
			at: note.updated_at,
			note,
		})),
	].sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime());

	const openNote = (notes.data ?? []).find((note) => note.id === openNoteId) ?? null;
	const viewing = (outputs.data ?? []).find((output) => output.id === openOutputId) ?? null;

	const openOutput = (output: StudioOutput) => {
		const action = studioOpenAction(output);
		if (action?.type === "audio-player") setPlaying(output);
		else if (action?.type === "studio-view") onOpenOutput(output.id);
	};

	const addNote = async () => {
		try {
			const note = await noteMutations.create.mutateAsync({ title: t("new_note"), content: "" });
			setOpenNoteId(note.id);
		} catch {
			toast.error(t("note_create_failed"));
		}
	};

	if (openNote) {
		return (
			<NoteEditor
				key={openNote.id}
				note={openNote}
				onBack={() => setOpenNoteId(null)}
				onSave={(changes) => noteMutations.update.mutateAsync({ noteId: openNote.id, ...changes })}
				onConvert={async () => {
					await noteMutations.convert.mutateAsync(openNote.id);
					toast.success(t("converted"));
				}}
				onDelete={async () => {
					await noteMutations.remove.mutateAsync(openNote.id);
					setOpenNoteId(null);
				}}
			/>
		);
	}

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
						workspaceId={workspaceId}
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
					workspaceId={workspaceId}
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
							{items.map((item) =>
								item.type === "output" ? (
									<StudioOutputRow
										key={`output-${item.output.id}`}
										output={item.output}
										onOpen={() => openOutput(item.output)}
										onRename={(title) =>
											studio.rename.mutateAsync({ outputId: item.output.id, title })
										}
										onDelete={() => studio.remove.mutateAsync(item.output.id)}
										onRetry={() =>
											studio.generate.mutate(
												{
													...(item.output.options as Record<string, unknown>),
													kind: item.output.kind,
													title: item.output.title,
													request_id: newRequestId(),
												},
												{
													onSuccess: (output) => notifications.markStarted(output.id),
													onError: () => toast.error(t("generate_failed")),
												}
											)
										}
									/>
								) : (
									<NoteRow
										key={`note-${item.note.id}`}
										note={item.note}
										onOpen={() => setOpenNoteId(item.note.id)}
									/>
								)
							)}
						</div>
					)}
				</div>
				{/* A strip of its own under the list: floating over it, the button
			    covered whichever output row scrolled beneath it. */}
				<div className="flex shrink-0 justify-center border-t px-3 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]">
					<Button
						className="rounded-full"
						onClick={addNote}
						disabled={noteMutations.create.isPending}
					>
						<NotebookPen className="size-4" />
						{t("add_note")}
					</Button>
				</div>
				<GenerateDialog
					kind={dialogKind}
					onOpenChange={(open) => {
						if (!open) setDialogKind(null);
					}}
					workspaceId={workspaceId}
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
