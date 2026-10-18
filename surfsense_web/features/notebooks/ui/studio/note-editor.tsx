"use client";

import { ArrowLeft, FileInput, Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { usePlateEditorComponent } from "@/components/editor/plate-editor-module";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import type { NotebookNote } from "../../model/notebook";

const SAVE_DELAY_MS = 800;

interface NoteEditorProps {
	note: NotebookNote;
	onBack: () => void;
	onSave: (changes: { title?: string; content?: string }) => Promise<unknown>;
	onConvert: () => Promise<unknown>;
	onDelete: () => Promise<unknown>;
	/** Where Back goes, for its label (the Studio panel unless said). */
	backLabel?: string;
	/** More actions beside Convert (the Notes page adds its own). */
	extraActions?: ReactNode;
}

/** The note, edited in place with the workspace's editor; saves as you type. */
export function NoteEditor({
	note,
	onBack,
	onSave,
	onConvert,
	onDelete,
	backLabel,
	extraActions,
}: NoteEditorProps) {
	const t = useTranslations("notebooks");
	const PlateEditor = usePlateEditorComponent();
	// The editor shows the note as it is now, a newer text included (a read
	// again, an earlier save landing), until the person types. From then on
	// it keeps the text it was showing: the note coming back from its own
	// save is older by the time it arrives, and loading it put an older text
	// in while typing went on, dropped the last words and moved the caret to
	// the start. The title likewise.
	const [heldContent, setHeldContent] = useState<string | null>(null);
	const [typedTitle, setTypedTitle] = useState<string | null>(null);
	const content = heldContent ?? note.content;
	const title = typedTitle ?? note.title;
	const [confirmingDelete, setConfirmingDelete] = useState(false);
	const [busy, setBusy] = useState(false);
	const pending = useRef<{ title?: string; content?: string }>({});
	const timer = useRef<number | null>(null);
	const saveRef = useRef(onSave);
	saveRef.current = onSave;

	/** Saves what is waiting now; resolves when that save is done (failed included). */
	const flush = (): Promise<unknown> => {
		if (timer.current) window.clearTimeout(timer.current);
		timer.current = null;
		const changes = pending.current;
		pending.current = {};
		if (Object.keys(changes).length === 0) return Promise.resolve();
		return saveRef.current(changes).catch(() => toast.error(t("note_save_failed")));
	};
	const queue = (changes: { title?: string; content?: string }) => {
		pending.current = { ...pending.current, ...changes };
		if (timer.current) window.clearTimeout(timer.current);
		timer.current = window.setTimeout(flush, SAVE_DELAY_MS);
	};
	// Leaving the editor must not drop the last keystrokes.
	// biome-ignore lint/correctness/useExhaustiveDependencies: flush reads refs only
	useEffect(
		() => () => {
			void flush();
		},
		[]
	);

	const run = async (action: () => Promise<unknown>, failure: string) => {
		setBusy(true);
		try {
			// The last keystrokes are saved first: converting reads the note as the server has it.
			await flush();
			await action();
		} catch {
			toast.error(failure);
		} finally {
			setBusy(false);
		}
	};

	return (
		<div className="flex min-h-0 flex-1 flex-col">
			<div className="flex items-center gap-1 px-3 pb-2">
				<Button
					variant="ghost"
					size="icon"
					className="size-8 shrink-0 rounded-full"
					onClick={() => {
						void flush();
						onBack();
					}}
					aria-label={backLabel ?? t("back_to_studio")}
				>
					<ArrowLeft className="size-4" />
				</Button>
				<Input
					value={title}
					onChange={(event) => {
						setTypedTitle(event.target.value);
						if (event.target.value.trim()) queue({ title: event.target.value.trim() });
					}}
					maxLength={300}
					aria-label={t("note_title")}
					className="h-8 border-transparent bg-transparent px-1 font-medium shadow-none focus-visible:border-input"
				/>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto px-2">
				{PlateEditor ? (
					<PlateEditor
						markdown={content}
						onMarkdownChange={(edited) => {
							setHeldContent((held) => held ?? content);
							queue({ content: edited });
						}}
						preset="note"
						chatMath
						chatCitations
						defaultEditing
						allowModeToggle={false}
						placeholder={t("note_placeholder")}
						editorVariant="default"
					/>
				) : (
					<Skeleton className="h-40 w-full" />
				)}
			</div>
			<div className="flex flex-wrap items-center justify-between gap-2 border-t px-3 py-2">
				{confirmingDelete ? (
					<div className="flex w-full items-center justify-between gap-2" role="alert">
						<span className="text-sm">{t("delete_note_confirm")}</span>
						<span className="flex gap-2">
							<Button size="sm" variant="ghost" onClick={() => setConfirmingDelete(false)}>
								{t("cancel")}
							</Button>
							<Button
								size="sm"
								variant="destructive"
								disabled={busy}
								onClick={() => run(onDelete, t("note_delete_failed"))}
							>
								{t("delete")}
							</Button>
						</span>
					</div>
				) : (
					<>
						<span className="flex flex-wrap items-center gap-2">
							<Button
								size="sm"
								variant="outline"
								disabled={busy || note.converted_document_id !== null}
								onClick={() => run(onConvert, t("convert_failed"))}
							>
								<FileInput className="size-4" />
								{note.converted_document_id !== null ? t("note_is_source") : t("convert_to_source")}
							</Button>
							{extraActions}
						</span>
						<Button
							size="sm"
							variant="ghost"
							className="text-destructive hover:text-destructive"
							onClick={() => setConfirmingDelete(true)}
						>
							<Trash2 className="size-4" />
							{t("delete")}
						</Button>
					</>
				)}
			</div>
		</div>
	);
}
