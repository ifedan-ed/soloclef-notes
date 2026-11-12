"use client";

import { NotebookPen, Plus, SearchX } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";

/** The list when there is nothing in it: no notes yet, or none that match. */
export function NotesEmpty({
	filtered,
	query,
	onNew,
	onClear,
	creating,
}: {
	filtered: boolean;
	query: string;
	onNew: () => void;
	onClear: () => void;
	creating: boolean;
}) {
	const t = useTranslations("notes");
	if (filtered) {
		return (
			<div className="flex flex-col items-center gap-3 px-6 py-14 text-center">
				<SearchX className="size-6 text-muted-foreground" aria-hidden />
				<p className="text-sm text-muted-foreground">
					{query ? t("empty_search", { query }) : t("empty_filtered")}
				</p>
				<Button size="sm" variant="outline" className="rounded-full" onClick={onClear}>
					{t("clear_filters")}
				</Button>
			</div>
		);
	}
	return (
		<div className="flex flex-col items-center gap-3 px-6 py-14 text-center">
			<span className="flex size-12 items-center justify-center rounded-2xl bg-muted text-muted-foreground md:hidden">
				<NotebookPen className="size-6" aria-hidden />
			</span>
			<p className="text-base font-semibold">{t("empty_title")}</p>
			<p className="max-w-xs text-sm leading-6 text-muted-foreground md:hidden">
				{t("empty_body")}
			</p>
			{/* Beside the list on a desktop, the open-note pane carries the button. */}
			<Button className="mt-1 rounded-full md:hidden" onClick={onNew} disabled={creating}>
				<Plus className="size-4" />
				{t("new_note")}
			</Button>
		</div>
	);
}
