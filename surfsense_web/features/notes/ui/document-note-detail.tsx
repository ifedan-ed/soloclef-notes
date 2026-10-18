"use client";

import { useQuery } from "@tanstack/react-query";
import { useAtomValue } from "jotai";
import { ArrowLeft, FileInput, FolderInput, Save, Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { canPerform, myAccessAtom } from "@/atoms/members/members-query.atoms";
import { usePlateEditorComponent } from "@/components/editor/plate-editor-module";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { notebooksApi } from "@/features/notebooks/api/notebooks-api";
import { useNotebooks } from "@/features/notebooks/hooks/use-notebook-queries";
import type { Notebook, NotebookNote } from "@/features/notebooks/model/notebook";
import { apiErrorDetail } from "@/features/workspace-team/lib/api-error-detail";
import { noteDocumentQuery } from "../api/note-queries";
import { notesApi } from "../api/notes-api";
import { useRefreshNotes } from "../hooks/use-workspace-notes";
import { documentNoteActions } from "../lib/note-places";
import { isUnsavedEdit } from "../lib/unsaved-edit";
import type { WorkspaceNote } from "../model/workspace-note";
import { NotebookPickerDialog } from "./notebook-picker-dialog";

type Picking = "move" | "source" | null;

interface DocumentNoteDetailProps {
	workspaceId: number;
	note: WorkspaceNote;
	onBack: () => void;
	/** It became a notebook's note: show that note instead. */
	onMoved: (note: NotebookNote, notebook: Notebook) => void;
}

/**
 * A note the assistant wrote as a file, which no notebook shows yet. Saved
 * with a button, since each save reindexes it; "Add to a notebook" moves it
 * into one (or copies it, see documentNoteActions), where it shows in the
 * Studio panel from then on.
 */
export function DocumentNoteDetail({
	workspaceId,
	note,
	onBack,
	onMoved,
}: DocumentNoteDetailProps) {
	const t = useTranslations("notes");
	const tn = useTranslations("notebooks");
	const refresh = useRefreshNotes(workspaceId);
	const content = useQuery(noteDocumentQuery(workspaceId, note.id));
	const PlateEditor = usePlateEditorComponent();
	// The editor shows the note's newest text until the person types, then
	// keeps the text it was showing (NoteEditor says why): Save's read again
	// must not load it under the person typing.
	const [heldText, setHeldText] = useState<string | null>(null);
	const { data: notebooks = [] } = useNotebooks(workspaceId);
	const { data: access } = useAtomValue(myAccessAtom);
	const actions = documentNoteActions(note, notebooks, {
		canDeleteDocuments: canPerform(access, "documents:delete"),
	});
	const [draft, setDraft] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	const [picking, setPicking] = useState<Picking>(null);
	const [confirmingDelete, setConfirmingDelete] = useState(false);

	const dirty = isUnsavedEdit(draft, content.data?.source_markdown);

	const save = async () => {
		if (!dirty || draft === null || !content.data) return;
		await notesApi.saveDocument(workspaceId, note.id, content.data.title, draft);
		setDraft(null);
		refresh();
	};
	const run = async (action: () => Promise<unknown>) => {
		setBusy(true);
		try {
			await action();
		} catch (error) {
			toast.error(apiErrorDetail(error, t("action_failed")));
		} finally {
			setBusy(false);
		}
	};

	if (content.isLoading) return <Skeleton className="m-4 h-48" />;
	if (!content.data) {
		return (
			<div className="space-y-3 p-4">
				<p className="text-sm text-muted-foreground">{t("open_failed")}</p>
				<Button size="sm" variant="outline" onClick={onBack}>
					{t("back_to_notes")}
				</Button>
			</div>
		);
	}
	const editorText = heldText ?? content.data.source_markdown;

	return (
		<div className="flex min-h-0 flex-1 flex-col">
			<div className="flex items-center gap-1 px-3 py-2">
				<Button
					variant="ghost"
					size="icon"
					className="size-8 shrink-0 rounded-full"
					onClick={onBack}
					aria-label={t("back_to_notes")}
				>
					<ArrowLeft className="size-4" />
				</Button>
				<h2 className="min-w-0 truncate px-1 font-medium">{note.title}</h2>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto px-2">
				{PlateEditor ? (
					<PlateEditor
						markdown={editorText}
						onMarkdownChange={(edited) => {
							setHeldText((held) => held ?? editorText);
							setDraft(edited);
						}}
						preset="note"
						chatMath
						chatCitations
						defaultEditing
						allowModeToggle={false}
						placeholder={tn("note_placeholder")}
						editorVariant="default"
					/>
				) : (
					<Skeleton className="h-40 w-full" />
				)}
			</div>
			<div className="flex flex-wrap items-center justify-between gap-2 border-t px-3 py-2">
				{confirmingDelete ? (
					<div className="flex w-full items-center justify-between gap-2" role="alert">
						<span className="text-sm">{tn("delete_note_confirm")}</span>
						<span className="flex gap-2">
							<Button size="sm" variant="ghost" onClick={() => setConfirmingDelete(false)}>
								{tn("cancel")}
							</Button>
							<Button
								size="sm"
								variant="destructive"
								disabled={busy}
								onClick={() =>
									run(async () => {
										await notesApi.deleteDocument(workspaceId, note.id);
										refresh();
										onBack();
									})
								}
							>
								{tn("delete")}
							</Button>
						</span>
					</div>
				) : (
					<>
						<span className="flex flex-wrap items-center gap-2">
							<Button
								size="sm"
								disabled={busy || !dirty}
								onClick={() =>
									run(async () => {
										await save();
										toast.success(t("saved"));
									})
								}
							>
								<Save className="size-4" />
								{t("save")}
							</Button>
							<Button
								size="sm"
								variant="outline"
								disabled={busy}
								onClick={() => setPicking("move")}
							>
								<FolderInput className="size-4" />
								{t("add_to_notebook")}
							</Button>
							<Button
								size="sm"
								variant="ghost"
								disabled={busy}
								onClick={() => setPicking("source")}
							>
								<FileInput className="size-4" />
								{tn("convert_to_source")}
							</Button>
						</span>
						{actions.canDelete ? (
							<Button
								size="sm"
								variant="ghost"
								className="text-destructive hover:text-destructive"
								onClick={() => setConfirmingDelete(true)}
							>
								<Trash2 className="size-4" />
								{tn("delete")}
							</Button>
						) : null}
					</>
				)}
			</div>
			<NotebookPickerDialog
				open={picking !== null}
				onOpenChange={(open) => !open && setPicking(null)}
				title={picking === "source" ? t("pick_notebook_source") : t("pick_notebook_add")}
				notebooks={picking === "source" ? actions.sourceTargets : notebooks}
				onPick={async (target) => {
					try {
						// Unsaved edits go with it.
						await save();
						if (picking === "source") {
							await notebooksApi.addSources(workspaceId, target.id, [
								{ kind: "document", id: note.id },
							]);
							refresh(target.id);
							toast.success(t("source_added", { notebook: target.title }));
							return;
						}
						const moved = await notesApi.moveIntoNotebook(workspaceId, note.id, target.id);
						refresh(target.id);
						toast.success(
							t(actions.addToNotebook === "move" ? "added_to" : "copied_to", {
								notebook: target.title,
							})
						);
						onMoved(moved, target);
					} catch (error) {
						toast.error(apiErrorDetail(error, t("action_failed")));
						throw error;
					}
				}}
			/>
		</div>
	);
}
