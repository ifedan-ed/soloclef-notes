"use client";

import { AlarmClock, FileText, MessageSquare, NotebookPen } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { Fragment, type ReactNode } from "react";
import { relativeTime } from "@/features/notebooks/lib/relative-time";
import { cn } from "@/lib/utils";
import type { NotesView } from "../lib/note-groups";
import type { WorkspaceNote } from "../model/workspace-note";

interface NoteListRowProps {
	note: WorkspaceNote;
	onOpen: () => void;
	/** The group heading above the row already names the notebook (by
	 *  folder) or the automation (by automation), so the row leaves it out. */
	groupedBy?: NotesView;
	/** It is being opened: its text and the editor are on their way. */
	busy?: boolean;
}

/** One note: title, opening words, where it lives, what made it, when. */
export function NoteListRow({ note, onOpen, groupedBy, busy = false }: NoteListRowProps) {
	const t = useTranslations("notes");
	const locale = useLocale();
	const Icon = note.kind === "document" ? FileText : NotebookPen;
	const made =
		note.origin === "automation"
			? t("by_automation", { automation: note.automation?.name ?? t("an_automation") })
			: note.origin === "chat"
				? t("from_chat")
				: t("by_hand");
	const MadeIcon = note.origin === "automation" ? AlarmClock : MessageSquare;
	const where = note.notebook
		? `${note.notebook.icon ? `${note.notebook.icon} ` : ""}${note.notebook.title}`
		: t("not_in_notebook");
	const sourceIn = note.source_in.map((ref) => ref.title).join(", ");

	const details: { key: string; node: ReactNode }[] = [];
	if (!(groupedBy === "folders" && note.notebook)) {
		details.push({
			key: "where",
			node: <span className={note.notebook ? "truncate" : "truncate italic"}>{where}</span>,
		});
	}
	if (groupedBy !== "automations") {
		details.push({
			key: "made",
			node: (
				<span className="inline-flex min-w-0 items-center gap-1">
					{note.origin === "hand" ? null : <MadeIcon className="size-3 shrink-0" aria-hidden />}
					<span className="truncate">{made}</span>
				</span>
			),
		});
	}
	if (sourceIn) {
		details.push({
			key: "source",
			node: <span className="truncate">{t("source_in", { notebooks: sourceIn })}</span>,
		});
	}
	details.push({
		key: "when",
		node: <time dateTime={note.created_at}>{relativeTime(note.created_at, locale)}</time>,
	});

	return (
		<button
			type="button"
			onClick={onOpen}
			aria-busy={busy}
			className={cn(
				"flex w-full min-w-0 items-start gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:outline-none",
				busy && "cursor-progress bg-accent"
			)}
		>
			<span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
				<Icon className="size-4" aria-hidden />
			</span>
			<span className="min-w-0 flex-1 space-y-0.5">
				<span className="block truncate text-sm font-medium">{note.title}</span>
				{note.preview ? (
					<span className="block truncate text-xs text-muted-foreground">{note.preview}</span>
				) : null}
				<span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
					{details.map((detail, index) => (
						<Fragment key={detail.key}>
							{index > 0 ? <span aria-hidden>·</span> : null}
							{detail.node}
						</Fragment>
					))}
				</span>
			</span>
		</button>
	);
}
