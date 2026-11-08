"use client";

import { NotebookPen } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { notePreview } from "../../lib/note-preview";
import { relativeTime } from "../../lib/relative-time";
import type { NotebookNote } from "../../model/notebook";

export function NoteRow({ note, onOpen }: { note: NotebookNote; onOpen: () => void }) {
	const t = useTranslations("notebooks");
	const locale = useLocale();
	const preview = notePreview(note.content);
	return (
		<button
			type="button"
			onClick={onOpen}
			className="flex w-full min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 text-left hover:bg-accent"
		>
			<span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
				<NotebookPen className="size-4" aria-hidden />
			</span>
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm font-medium">{note.title}</span>
				<span className="block truncate text-xs text-muted-foreground">
					{[
						t("note"),
						note.converted_document_id ? t("note_is_source") : null,
						preview.slice(0, 80) || null,
						relativeTime(note.updated_at, locale),
					]
						.filter(Boolean)
						.join(" · ")}
				</span>
			</span>
		</button>
	);
}
