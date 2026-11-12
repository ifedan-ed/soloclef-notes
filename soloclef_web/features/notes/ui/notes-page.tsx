"use client";

import { NotebookPen, Plus } from "lucide-react";
import { usePathname } from "next/navigation";
import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { NOTES_PAGE } from "@/lib/dashboard-paths";
import { cn } from "@/lib/utils";
import { useNoteActions } from "../hooks/use-note-actions";
import { useNoteOpening } from "../hooks/use-note-opening";
import { useNotesList } from "../hooks/use-notes-list";
import { noteIdFromPath, notePath } from "../lib/note-paths";
import type { NotesFilter } from "../lib/notes-filter";
import { NotePane } from "./note-pane";
import { NotesListPane } from "./notes-list-pane";

/**
 * The Notes place: every note the account has, and one open beside them.
 *
 * Each note has its own address, /dashboard/notes/{id}, so a note can be
 * linked, bookmarked, opened in a second tab and left with Back. Moving
 * between notes changes the address with the History API rather than a
 * navigation, which Next.js keeps in step with usePathname: the list keeps
 * its scroll, its search and its filters, and nothing is mounted again.
 * Mount this one component at /dashboard/notes and every address under it
 * (an optional catch-all route).
 *
 * On a desktop it is two panes, the list and the note at reading width. At
 * phone width the list and the note are two screens.
 */
export function NotesPage() {
	const t = useTranslations("notes");
	const noteId = noteIdFromPath(usePathname());
	const [filter, setFilter] = useState<NotesFilter>({});
	const [creating, setCreating] = useState(false);
	const actions = useNoteActions();
	// Whether the account has any note at all (the list's own first page when
	// it is not filtered), so the empty pane can welcome rather than say "pick".
	const everything = useNotesList({});
	const hasNotes = (everything.data?.pages[0]?.total ?? 1) > 0;

	const show = useCallback((id: number) => {
		window.history.pushState(null, "", notePath(id));
	}, []);
	// A clicked note takes the pane once it can be drawn whole; until then
	// the list keeps what it shows and marks the row that was clicked.
	const { opening, open } = useNoteOpening(show);
	const close = useCallback(() => {
		window.history.pushState(null, "", NOTES_PAGE);
	}, []);

	// A new note goes where the list is looking: into the notebook it is
	// filtered to, else into none.
	const createNote = async () => {
		setCreating(true);
		try {
			const note = await actions.create({
				notebook_id: typeof filter.notebook === "number" ? filter.notebook : null,
			});
			open(note.id);
		} catch {
			toast.error(t("create_failed"));
		} finally {
			setCreating(false);
		}
	};

	return (
		<div className="flex h-full min-h-0 w-full min-w-0 overflow-hidden">
			<NotesListPane
				filter={filter}
				onFilterChange={setFilter}
				selectedId={opening ?? noteId}
				onOpen={open}
				onNew={() => void createNote()}
				creating={creating}
				className={cn(
					"w-full md:w-[22rem] md:shrink-0 md:border-r lg:w-[24rem]",
					noteId !== null && "hidden md:flex"
				)}
			/>
			<section
				className={cn(
					"min-h-0 min-w-0 flex-1 flex-col bg-background",
					noteId === null ? "hidden md:flex" : "flex"
				)}
				aria-label={t("open_note")}
			>
				{noteId !== null ? (
					<NotePane
						key={noteId}
						noteId={noteId}
						onBack={close}
						backLabel={t("back_to_notes")}
						onDeleted={close}
					/>
				) : (
					<div className="flex flex-1 flex-col items-center justify-center gap-3 px-8 text-center">
						<span className="flex size-12 items-center justify-center rounded-2xl bg-muted text-muted-foreground">
							<NotebookPen className="size-6" aria-hidden />
						</span>
						<p className="text-base font-medium">
							{hasNotes ? t("none_open_title") : t("empty_title")}
						</p>
						<p className="max-w-sm text-sm leading-6 text-muted-foreground">
							{hasNotes ? t("none_open_body") : t("empty_body")}
						</p>
						<Button
							variant={hasNotes ? "outline" : "default"}
							className="mt-1 rounded-full"
							onClick={() => void createNote()}
							disabled={creating}
						>
							<Plus className="size-4" />
							{t("new_note")}
						</Button>
					</div>
				)}
			</section>
		</div>
	);
}
