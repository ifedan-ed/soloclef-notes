"use client";

import { ArrowLeft, FileQuestion } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { useNote } from "../hooks/use-note";
import { NoteView } from "./note-view";

/** The note an address names, read and then open; or why it cannot be. */
export function NotePane({
	noteId,
	onBack,
	backLabel,
	alwaysShowBack,
	onDeleted,
	compact,
}: {
	noteId: number;
	onBack: () => void;
	backLabel: string;
	alwaysShowBack?: boolean;
	onDeleted: () => void;
	compact?: boolean;
}) {
	const t = useTranslations("notes");
	const note = useNote(noteId);
	const showSkeleton = useDelayedFlag(note.isLoading);

	if (note.data) {
		return (
			<NoteView
				key={note.data.id}
				note={note.data}
				onBack={onBack}
				backLabel={backLabel}
				alwaysShowBack={alwaysShowBack}
				onDeleted={onDeleted}
				compact={compact}
			/>
		);
	}
	if (note.isLoading) {
		return showSkeleton ? (
			<div className="mx-auto w-full max-w-3xl space-y-4 px-5 pt-20 sm:px-10" aria-busy>
				<Skeleton className="h-9 w-2/3" />
				<Skeleton className="h-3 w-40" />
				<Skeleton className="mt-6 h-4 w-11/12" />
				<Skeleton className="h-4 w-10/12" />
				<Skeleton className="h-4 w-8/12" />
			</div>
		) : null;
	}
	return (
		<div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 text-center">
			<FileQuestion className="size-7 text-muted-foreground" aria-hidden />
			<p className="text-sm text-muted-foreground">{t("open_failed")}</p>
			<Button size="sm" variant="outline" className="rounded-full" onClick={onBack}>
				<ArrowLeft className="size-4" />
				{backLabel}
			</Button>
		</div>
	);
}
