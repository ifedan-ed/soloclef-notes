"use client";

import { BookOpenCheck, Bot, MessageSquareText, NotebookText } from "lucide-react";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";
import type { NoteNotebook, NoteOrigin } from "../model/note";

/** The notebook a note is filed in, with its emoji; or that it is in none. */
export function NotebookLabel({
	notebook,
	className,
}: {
	notebook: NoteNotebook | null;
	className?: string;
}) {
	const t = useTranslations("notes");
	if (!notebook) {
		return <span className={cn("truncate", className)}>{t("no_notebook")}</span>;
	}
	return (
		<span className={cn("inline-flex min-w-0 items-center gap-1", className)}>
			{notebook.icon ? (
				<span aria-hidden className="shrink-0">
					{notebook.icon}
				</span>
			) : (
				<NotebookText className="size-3.5 shrink-0" aria-hidden />
			)}
			<span className="truncate">{notebook.title}</span>
		</span>
	);
}

/** Where a note came from, said only when it was not written by hand. */
export function OriginLabel({ origin, className }: { origin: NoteOrigin; className?: string }) {
	const t = useTranslations("notes");
	if (origin === "hand") return null;
	const Icon = origin === "chat" ? MessageSquareText : Bot;
	return (
		<span className={cn("inline-flex shrink-0 items-center gap-1", className)}>
			<Icon className="size-3.5" aria-hidden />
			{origin === "chat" ? t("from_chat") : t("from_automation")}
		</span>
	);
}

/** A small mark for a note some notebook's chat reads. */
export function SourceMark({ count, className }: { count: number; className?: string }) {
	const t = useTranslations("notes");
	if (count === 0) return null;
	return (
		<span
			className={cn("inline-flex shrink-0 items-center gap-1", className)}
			title={t("source_of_count", { count })}
		>
			<BookOpenCheck className="size-3.5" aria-hidden />
			<span className="sr-only">{t("source_of_count", { count })}</span>
		</span>
	);
}
