"use client";

import { Library } from "lucide-react";
import { useTranslations } from "next-intl";
import { useNotebookChat } from "../state/notebook-chat-context";

/** "N sources" in the composer of a notebook chat; nothing elsewhere. */
export function NotebookSourcesChip() {
	const notebook = useNotebookChat();
	const t = useTranslations("notebooks");
	if (!notebook) return null;
	return (
		<button
			type="button"
			onClick={notebook.openSources}
			className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-full border border-primary/10 bg-secondary/50 px-3 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
			aria-label={t("composer_sources", { count: notebook.enabledSourceCount })}
		>
			<Library className="size-3.5" aria-hidden />
			{t("composer_sources", { count: notebook.enabledSourceCount })}
		</button>
	);
}
