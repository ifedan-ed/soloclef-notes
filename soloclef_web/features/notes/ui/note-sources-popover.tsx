"use client";

import { BookOpenCheck } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { useNotebooks } from "@/features/notebooks/hooks/use-notebook-queries";
import { cn } from "@/lib/utils";
import { useNoteActions } from "../hooks/use-note-actions";
import type { Note } from "../model/note";

/**
 * The source switch, one per notebook: whether that notebook's chat reads
 * this note. Nothing is copied; the chat reads the note as it is now.
 */
export function NoteSourcesPopover({ note }: { note: Note }) {
	const t = useTranslations("notes");
	const { data: notebooks = [] } = useNotebooks();
	const actions = useNoteActions();
	const [busy, setBusy] = useState<number | null>(null);
	const on = new Set(note.sources.map((source) => source.id));
	// The notebook it is filed in first, then the ones already reading it.
	const ordered = [...notebooks].sort(
		(a, b) =>
			Number(b.id === note.notebook?.id) - Number(a.id === note.notebook?.id) ||
			Number(on.has(b.id)) - Number(on.has(a.id))
	);

	const toggle = async (notebookId: number, next: boolean) => {
		setBusy(notebookId);
		try {
			await actions.setSource(note.id, notebookId, next);
		} catch {
			toast.error(t("source_failed"));
		} finally {
			setBusy(null);
		}
	};

	return (
		<Popover>
			<PopoverTrigger asChild>
				<Button
					variant="ghost"
					size="sm"
					className={cn("h-8 shrink-0 gap-1.5 rounded-full px-2.5", on.size > 0 && "text-primary")}
					aria-label={t("sources_button")}
				>
					<BookOpenCheck className="size-4" />
					<span className="hidden text-xs sm:inline">
						{on.size > 0 ? t("source_of_count", { count: on.size }) : t("sources_button")}
					</span>
				</Button>
			</PopoverTrigger>
			<PopoverContent align="end" className="w-80 p-0">
				<div className="border-b px-4 py-3">
					<p className="text-sm font-medium">{t("sources_title")}</p>
					<p className="mt-1 text-xs leading-5 text-muted-foreground">{t("sources_body")}</p>
				</div>
				<div className="max-h-72 overflow-y-auto py-1">
					{ordered.length === 0 ? (
						<p className="px-4 py-3 text-sm text-muted-foreground">{t("no_notebooks")}</p>
					) : (
						ordered.map((notebook) => {
							const id = `note-source-${note.id}-${notebook.id}`;
							return (
								<label
									key={notebook.id}
									htmlFor={id}
									className="flex cursor-pointer items-center gap-3 px-4 py-2 hover:bg-accent/60"
								>
									<span aria-hidden className="w-5 shrink-0 text-center">
										{notebook.icon ?? "📓"}
									</span>
									<span className="min-w-0 flex-1 truncate text-sm">{notebook.title}</span>
									<Switch
										id={id}
										checked={on.has(notebook.id)}
										disabled={busy === notebook.id}
										onCheckedChange={(next) => void toggle(notebook.id, next)}
									/>
								</label>
							);
						})
					)}
				</div>
			</PopoverContent>
		</Popover>
	);
}
