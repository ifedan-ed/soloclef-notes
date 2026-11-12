"use client";

import { Pin } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { Switch } from "@/components/ui/switch";
import { useNoteActions } from "@/features/notes/hooks/use-note-actions";
import { previewLine } from "@/features/notes/lib/note-text";
import type { NoteSummary } from "@/features/notes/model/note";
import { OriginLabel } from "@/features/notes/ui/note-where";
import { relativeTime } from "../../lib/relative-time";

/**
 * One of a notebook's notes in its Notes tab, with the switch that decides
 * whether this notebook's chat reads it. The switch is the note itself
 * becoming a source, not a copy of it.
 */
export function NotebookNoteRow({
	note,
	notebookId,
	onOpen,
}: {
	note: NoteSummary;
	notebookId: number;
	onOpen: () => void;
}) {
	const t = useTranslations("notes");
	const locale = useLocale();
	const actions = useNoteActions();
	const [busy, setBusy] = useState(false);
	const isSource = note.sources.some((source) => source.id === notebookId);
	const preview = previewLine(note.preview);
	const switchId = `notebook-${notebookId}-note-${note.id}-source`;

	const toggle = async (on: boolean) => {
		setBusy(true);
		try {
			await actions.setSource(note.id, notebookId, on);
		} catch {
			toast.error(t("source_failed"));
		} finally {
			setBusy(false);
		}
	};

	return (
		<div className="group flex items-start gap-2 rounded-xl px-2 py-2 hover:bg-accent/60">
			<button type="button" onClick={onOpen} className="min-w-0 flex-1 space-y-0.5 text-left">
				<span className="flex min-w-0 items-center gap-1.5">
					{note.pinned ? (
						<Pin className="size-3 shrink-0 fill-current text-primary" aria-label={t("pinned")} />
					) : null}
					<span className="truncate text-sm font-medium">{note.title}</span>
				</span>
				<span className="line-clamp-2 text-xs leading-5 text-muted-foreground">
					{preview || <span className="italic">{t("empty_note")}</span>}
				</span>
				<span className="flex items-center gap-2 text-[11px] text-muted-foreground/90">
					<OriginLabel origin={note.origin} />
					<span className="tabular-nums">{relativeTime(note.updated_at, locale)}</span>
				</span>
			</button>
			<span className="flex shrink-0 flex-col items-center gap-0.5 pt-0.5">
				<Switch
					id={switchId}
					checked={isSource}
					disabled={busy}
					onCheckedChange={(on) => void toggle(on)}
					className="h-5 w-9 [&>span]:size-4 [&>span]:data-[state=checked]:translate-x-4"
					aria-label={t("use_as_source_in_this_notebook")}
				/>
				<label htmlFor={switchId} className="cursor-pointer text-[10px] text-muted-foreground">
					{t("source_switch_label")}
				</label>
			</span>
		</div>
	);
}
