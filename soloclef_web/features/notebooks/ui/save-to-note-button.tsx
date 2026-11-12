"use client";

import { useAuiState } from "@assistant-ui/react";
import { useSetAtom } from "jotai";
import { NotebookPen, NotebookText } from "lucide-react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { savedMessageId } from "@/features/chat-thread/regenerated-question";
import { useNoteActions } from "@/features/notes/hooks/use-note-actions";
import { useNotesList } from "@/features/notes/hooks/use-notes-list";
import { notePath } from "@/features/notes/lib/note-paths";
import { noteFromAnswer } from "../lib/note-from-answer";
import { useNotebookChat } from "../state/notebook-chat-context";
import { noteToOpenAtom } from "../state/open-note.atom";

/** Where the notebook's panels sit side by side (Tailwind's lg). */
const PANELS_SIDE_BY_SIDE = "(min-width: 1024px)";

/**
 * Save to note, under every answer of a notebook's chat. The note is filed
 * in this notebook and remembers the conversation and the answer, so it
 * links back to the chat, and an answer already kept says so and opens its
 * note instead of keeping a second copy.
 */
export function SaveToNoteButton({ markdown }: { markdown: string }) {
	const notebook = useNotebookChat();
	const t = useTranslations("notes");
	const actions = useNoteActions();
	const messageId = savedMessageId(useAuiState(({ message }) => message?.id));
	const threadId = notebook?.threadId ?? null;
	// One read per conversation, shared by every answer in it.
	const kept = useNotesList(
		{ threadId: threadId ?? 0, origin: "chat" },
		Boolean(notebook && threadId !== null)
	);
	const [saving, setSaving] = useState(false);
	const openInPanel = useSetAtom(noteToOpenAtom);
	const router = useRouter();
	if (!notebook) return null;

	const keptNote =
		messageId === null
			? undefined
			: kept.data?.pages
					.flatMap((page) => page.items)
					.find((note) => note.message_id === messageId);
	// Beside the chat in the Notes tab where the panels sit side by side; on
	// a phone the chat fills the screen, so the note opens in the Notes place.
	const openNote = (noteId: number) => {
		if (window.matchMedia(PANELS_SIDE_BY_SIDE).matches) {
			openInPanel({ notebookId: notebook.notebookId, noteId });
		} else {
			router.push(notePath(noteId));
		}
	};

	const save = async () => {
		if (!markdown || saving) return;
		setSaving(true);
		try {
			const { title, content } = noteFromAnswer(markdown);
			const note = await actions.create({
				title: title || undefined,
				content,
				notebook_id: notebook.notebookId,
				...(threadId !== null ? { thread_id: threadId } : {}),
				...(threadId !== null && messageId !== null ? { message_id: messageId } : {}),
			});
			toast.success(t("saved_to_notes"), {
				action: { label: t("open"), onClick: () => openNote(note.id) },
			});
		} catch {
			toast.error(t("save_to_note_failed"));
		} finally {
			setSaving(false);
		}
	};

	if (keptNote) {
		return (
			<TooltipIconButton tooltip={t("saved_open_note")} onClick={() => openNote(keptNote.id)}>
				<NotebookText className="text-primary" />
			</TooltipIconButton>
		);
	}
	return (
		<TooltipIconButton tooltip={t("save_to_note")} onClick={save} disabled={!markdown || saving}>
			<NotebookPen />
		</TooltipIconButton>
	);
}
