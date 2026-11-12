"use client";

import { NotebookPen, Plus, RefreshCw } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { useNoteActions } from "@/features/notes/hooks/use-note-actions";
import { useNoteOpening } from "@/features/notes/hooks/use-note-opening";
import { useNotesList } from "@/features/notes/hooks/use-notes-list";
import { NotePane } from "@/features/notes/ui/note-pane";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { NotebookNoteRow } from "./notebook-note-row";

const SKELETON_KEYS = ["a", "b", "c"];

/**
 * A notebook's Notes tab: the notes filed in it, a new one written straight
 * into it, and each note's source switch. A note opens here, in the panel
 * the workspace widens for it, and Back returns to the list.
 */
export function NotebookNotesTab({
	notebookId,
	openNoteId,
	onOpenNote,
}: {
	notebookId: number;
	openNoteId: number | null;
	onOpenNote: (noteId: number | null) => void;
}) {
	const t = useTranslations("notes");
	const list = useNotesList({ notebook: notebookId });
	const actions = useNoteActions();
	const [creating, setCreating] = useState(false);
	const showSkeleton = useDelayedFlag(list.isLoading);
	// The list stays until the clicked note can be drawn whole (and the
	// editor's code is asked for as this tab mounts).
	const { open } = useNoteOpening(onOpenNote);
	const notes = list.data?.pages.flatMap((page) => page.items) ?? [];

	const createNote = async () => {
		setCreating(true);
		try {
			const note = await actions.create({ notebook_id: notebookId });
			onOpenNote(note.id);
		} catch {
			toast.error(t("create_failed"));
		} finally {
			setCreating(false);
		}
	};

	if (openNoteId !== null) {
		return (
			<NotePane
				key={openNoteId}
				noteId={openNoteId}
				onBack={() => onOpenNote(null)}
				backLabel={t("back_to_notebook_notes")}
				alwaysShowBack
				compact
				onDeleted={() => onOpenNote(null)}
			/>
		);
	}

	return (
		<div className="flex min-h-0 flex-1 flex-col">
			<div className="px-3 pb-2">
				<Button
					variant="outline"
					className="h-9 w-full rounded-full"
					onClick={() => void createNote()}
					disabled={creating}
				>
					{creating ? <Spinner size="xs" /> : <Plus className="size-4" />}
					{t("new_note_here")}
				</Button>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-3">
				{list.isError && !list.data ? (
					<div className="m-2 flex items-center gap-3 rounded-xl border p-3 text-sm">
						<span className="flex-1 text-muted-foreground">{t("load_failed")}</span>
						<Button size="sm" variant="outline" onClick={() => void list.refetch()}>
							<RefreshCw className="size-4" />
							{t("retry")}
						</Button>
					</div>
				) : list.isLoading ? (
					showSkeleton ? (
						<div className="space-y-2 px-1.5" aria-busy>
							{SKELETON_KEYS.map((key) => (
								<Skeleton key={key} className="h-16 w-full rounded-xl" />
							))}
						</div>
					) : null
				) : notes.length === 0 ? (
					<div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
						<NotebookPen className="size-6 text-muted-foreground" aria-hidden />
						<p className="text-sm font-medium">{t("notebook_empty_title")}</p>
						<p className="text-xs leading-5 text-muted-foreground">{t("notebook_empty_body")}</p>
					</div>
				) : (
					<>
						<p className="px-2 pb-1.5 text-[11px] leading-4 text-muted-foreground">
							{t("notebook_source_hint")}
						</p>
						<ul className="space-y-0.5">
							{notes.map((note) => (
								<li key={note.id}>
									<NotebookNoteRow
										note={note}
										notebookId={notebookId}
										onOpen={() => open(note.id)}
									/>
								</li>
							))}
						</ul>
						{list.hasNextPage ? (
							<div className="flex justify-center pt-2">
								<Button
									size="sm"
									variant="ghost"
									className="rounded-full"
									disabled={list.isFetchingNextPage}
									onClick={() => void list.fetchNextPage()}
								>
									{t("show_more", { count: (list.data?.pages[0]?.total ?? 0) - notes.length })}
								</Button>
							</div>
						) : null}
					</>
				)}
			</div>
		</div>
	);
}
