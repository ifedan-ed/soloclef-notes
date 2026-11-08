"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CopyPlus, ExternalLink } from "lucide-react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { notebooksApi } from "@/features/notebooks/api/notebooks-api";
import { useNotebooks } from "@/features/notebooks/hooks/use-notebook-queries";
import { saveNotebookNote } from "@/features/notebooks/lib/note-saves";
import { NoteEditor } from "@/features/notebooks/ui/studio/note-editor";
import { apiErrorDetail } from "@/features/workspace-team/lib/api-error-detail";
import { notebookNoteQuery } from "../api/note-queries";
import { useRefreshNotes } from "../hooks/use-workspace-notes";
import { noteInNotebookHref, otherNotebooks } from "../lib/note-places";
import type { WorkspaceNote } from "../model/workspace-note";
import { NotebookPickerDialog } from "./notebook-picker-dialog";

interface NotebookNoteDetailProps {
	workspaceId: number;
	note: WorkspaceNote;
	onBack: () => void;
}

/** A notebook's note, edited as in its Studio panel, plus Open and Copy. */
export function NotebookNoteDetail({ workspaceId, note, onBack }: NotebookNoteDetailProps) {
	const t = useTranslations("notes");
	const queryClient = useQueryClient();
	const refresh = useRefreshNotes(workspaceId);
	const notebookId = note.notebook?.id ?? 0;
	const opened = useQuery(notebookNoteQuery(workspaceId, notebookId, note.id));
	const { data: notebooks = [] } = useNotebooks(workspaceId);
	const [copying, setCopying] = useState(false);
	const href = noteInNotebookHref(workspaceId, note);

	const back = () => {
		refresh(notebookId);
		onBack();
	};

	if (opened.isLoading) return <Skeleton className="m-4 h-48" />;
	if (!opened.data) {
		return (
			<div className="space-y-3 p-4">
				<p className="text-sm text-muted-foreground">{t("open_failed")}</p>
				<Button size="sm" variant="outline" onClick={back}>
					{t("back_to_notes")}
				</Button>
			</div>
		);
	}

	return (
		<>
			<NoteEditor
				key={opened.data.id}
				note={opened.data}
				backLabel={t("back_to_notes")}
				onBack={back}
				// This note and its notebook's Studio list take the text as the save starts.
				onSave={(changes) =>
					saveNotebookNote(
						queryClient,
						{ workspaceId, notebookId, noteId: note.id },
						changes,
						(body) => notebooksApi.updateNote(workspaceId, notebookId, note.id, body)
					)
				}
				onConvert={async () => {
					await notebooksApi.convertNote(workspaceId, notebookId, note.id);
					await opened.refetch();
					refresh(notebookId);
					toast.success(t("source_added", { notebook: note.notebook?.title ?? "" }));
				}}
				onDelete={async () => {
					await notebooksApi.deleteNote(workspaceId, notebookId, note.id);
					refresh(notebookId);
					onBack();
				}}
				extraActions={
					<>
						{href ? (
							<Button asChild size="sm" variant="ghost">
								<Link href={href}>
									<ExternalLink className="size-4" />
									{t("open_in_notebook")}
								</Link>
							</Button>
						) : null}
						<Button size="sm" variant="ghost" onClick={() => setCopying(true)}>
							<CopyPlus className="size-4" />
							{t("copy_to_notebook")}
						</Button>
					</>
				}
			/>
			<NotebookPickerDialog
				open={copying}
				onOpenChange={setCopying}
				title={t("pick_notebook_copy")}
				notebooks={otherNotebooks(notebooks, note)}
				onPick={async (target) => {
					try {
						await notebooksApi.copyNote(workspaceId, notebookId, note.id, target.id);
					} catch (error) {
						toast.error(apiErrorDetail(error, t("action_failed")));
						throw error;
					}
					refresh(target.id);
					toast.success(t("copied_to", { notebook: target.title }));
				}}
			/>
		</>
	);
}
