"use client";

import { CheckIcon, NotebookPen } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { useNotebookChat } from "../state/notebook-chat-context";

/** Keep an assistant answer as a notebook note; rendered only in notebook chats. */
export function SaveToNoteButton({ markdown }: { markdown: string }) {
	const notebook = useNotebookChat();
	const t = useTranslations("notebooks");
	const [saved, setSaved] = useState(false);
	const [saving, setSaving] = useState(false);
	if (!notebook) return null;

	const save = async () => {
		if (!markdown || saving) return;
		setSaving(true);
		try {
			await notebook.saveToNote(markdown);
			setSaved(true);
			window.setTimeout(() => setSaved(false), 1500);
		} catch {
			toast.error(t("save_to_note_failed"));
		} finally {
			setSaving(false);
		}
	};

	return (
		<TooltipIconButton tooltip={t("save_to_note")} onClick={save} disabled={!markdown || saving}>
			{saved ? <CheckIcon /> : <NotebookPen />}
		</TooltipIconButton>
	);
}
