"use client";

import { Pin } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { relativeTime } from "@/features/notebooks/lib/relative-time";
import { cn } from "@/lib/utils";
import { previewLine } from "../lib/note-text";
import type { NoteSummary } from "../model/note";
import { NotebookLabel, OriginLabel, SourceMark } from "./note-where";

interface NoteListRowProps {
	note: NoteSummary;
	selected: boolean;
	onOpen: () => void;
	/** In one notebook's own list the notebook goes without saying. */
	showNotebook?: boolean;
}

/** One note in a list: its title, two lines of its text, where it lives and when it was edited. */
export function NoteListRow({ note, selected, onOpen, showNotebook = true }: NoteListRowProps) {
	const t = useTranslations("notes");
	const locale = useLocale();
	const preview = previewLine(note.preview);
	return (
		<button
			type="button"
			onClick={onOpen}
			aria-current={selected ? "page" : undefined}
			className={cn(
				"group flex w-full min-w-0 flex-col gap-1 rounded-xl px-3 py-2.5 text-left transition-colors",
				"focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
				selected ? "bg-accent" : "hover:bg-accent/60"
			)}
		>
			<span className="flex min-w-0 items-center gap-1.5">
				{note.pinned ? (
					<Pin className="size-3.5 shrink-0 fill-current text-primary" aria-label={t("pinned")} />
				) : null}
				<span className="truncate text-sm font-medium text-foreground">{note.title}</span>
			</span>
			<span
				className={cn(
					"line-clamp-2 text-[13px] leading-5 text-muted-foreground",
					!preview && "italic"
				)}
			>
				{preview || t("empty_note")}
			</span>
			<span className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground/90">
				{showNotebook ? <NotebookLabel notebook={note.notebook} className="max-w-[55%]" /> : null}
				<OriginLabel origin={note.origin} />
				<SourceMark count={note.sources.length} />
				<span className="ml-auto shrink-0 tabular-nums">
					{relativeTime(note.updated_at, locale)}
				</span>
			</span>
		</button>
	);
}
