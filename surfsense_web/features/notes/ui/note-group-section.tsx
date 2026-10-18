"use client";

import { AlarmClock, ChevronRight, FileText, Folder, NotebookText } from "lucide-react";
import { useTranslations } from "next-intl";
import { useId } from "react";
import { cn } from "@/lib/utils";
import type { NoteGroup, NotesView } from "../lib/note-groups";
import type { WorkspaceNote } from "../model/workspace-note";
import { NoteListRow } from "./note-list-row";

interface NoteGroupSectionProps {
	group: NoteGroup;
	view: NotesView;
	open: boolean;
	onToggle: () => void;
	/** The note being opened (use-note-opening.ts), whose row says so. */
	opening?: WorkspaceNote | null;
	onOpenNote: (note: WorkspaceNote) => void;
}

/** One notebook's, folder's or automation's notes under a heading that folds them away. */
export function NoteGroupSection({
	group,
	view,
	open,
	onToggle,
	opening = null,
	onOpenNote,
}: NoteGroupSectionProps) {
	const t = useTranslations("notes");
	const title =
		group.title ?? (group.kind === "no_folder" ? t("no_folder") : t("deleted_automation"));
	// A group key can hold an automation's name, which is no valid element id.
	const listId = useId();

	return (
		<section>
			<button
				type="button"
				onClick={onToggle}
				aria-expanded={open}
				aria-controls={listId}
				className="flex w-full min-w-0 items-center gap-2 rounded-md py-1 text-left text-xs font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:text-foreground focus-visible:outline-none"
			>
				<ChevronRight
					className={cn(
						"size-3.5 shrink-0 transition-transform motion-reduce:transition-none",
						open && "rotate-90"
					)}
					aria-hidden
				/>
				<GroupIcon group={group} />
				<span className="truncate">{title}</span>
				<span className="shrink-0 tabular-nums">{group.notes.length}</span>
			</button>
			{open ? (
				<ul id={listId} className="-mx-3 mt-1 space-y-0.5" aria-label={title}>
					{group.notes.map((note) => (
						<li key={`${note.kind}-${note.id}`}>
							<NoteListRow
								note={note}
								groupedBy={view}
								busy={opening?.kind === note.kind && opening.id === note.id}
								onOpen={() => onOpenNote(note)}
							/>
						</li>
					))}
				</ul>
			) : null}
		</section>
	);
}

function GroupIcon({ group }: { group: NoteGroup }) {
	if (group.kind === "notebook" && group.icon) {
		return (
			<span className="shrink-0 text-sm leading-none" aria-hidden>
				{group.icon}
			</span>
		);
	}
	const Icon =
		group.kind === "notebook"
			? NotebookText
			: group.kind === "automation"
				? AlarmClock
				: group.kind === "folder"
					? Folder
					: FileText;
	return <Icon className="size-3.5 shrink-0" aria-hidden />;
}
