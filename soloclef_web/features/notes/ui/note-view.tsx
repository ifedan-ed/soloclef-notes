"use client";

import { ArrowLeft, MessageSquareText, Pin } from "lucide-react";
import Link from "next/link";
import { useLocale, useTranslations } from "next-intl";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { usePlateEditorComponent } from "@/components/editor/plate-editor-module";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { relativeTime } from "@/features/notebooks/lib/relative-time";
import { notebookPath } from "@/lib/dashboard-paths";
import { cn } from "@/lib/utils";
import { useNoteActions } from "../hooks/use-note-actions";
import { useNoteAutosave } from "../hooks/use-note-autosave";
import type { Note } from "../model/note";
import { NoteMenu } from "./note-menu";
import { NoteSaveStatus } from "./note-save-status";
import { NoteSourcesPopover } from "./note-sources-popover";
import { NoteTitleField } from "./note-title-field";
import { NotebookLabel } from "./note-where";

interface NoteViewProps {
	note: Note;
	/** Back to the list; shown on a phone always, and on a desktop when `alwaysShowBack`. */
	onBack?: () => void;
	backLabel: string;
	alwaysShowBack?: boolean;
	onDeleted: () => void;
	/** A narrower frame (a notebook's panel): less padding, a smaller title. */
	compact?: boolean;
}

/**
 * One note, open to read and write. The title and the text save themselves
 * as they are typed; the bar above says whether they are saved and whether
 * the assistant has caught up, and holds the pin, the source switch and the
 * rest. Mounted once per note (keyed by its id): the editor takes the text
 * once and from then on the person's typing is the text, so a save coming
 * back can never move the caret or undo a word.
 */
export function NoteView({
	note,
	onBack,
	backLabel,
	alwaysShowBack = false,
	onDeleted,
	compact = false,
}: NoteViewProps) {
	const t = useTranslations("notes");
	const locale = useLocale();
	const PlateEditor = usePlateEditorComponent();
	const autosave = useNoteAutosave(note.id);
	const actions = useNoteActions();
	const [title, setTitle] = useState(note.title);
	const [initialText] = useState(note.content);
	const [pinning, setPinning] = useState(false);
	const editorRef = useRef<HTMLDivElement>(null);
	// Every save puts the server's answer in the note's cache, so this is the
	// note as last saved; only the title and text on screen are the person's.
	const current = note;

	const togglePin = async () => {
		setPinning(true);
		try {
			await actions.setPinned(note.id, !current.pinned);
		} catch {
			toast.error(t("action_failed"));
		} finally {
			setPinning(false);
		}
	};

	const fromChat = current.origin === "chat";
	const chatHref = current.chat_notebook_id ? notebookPath(current.chat_notebook_id) : null;

	return (
		<article className="flex min-h-0 min-w-0 flex-1 flex-col">
			<div className="flex h-12 shrink-0 items-center gap-1 border-b px-2 sm:px-3">
				{onBack ? (
					<Button
						variant="ghost"
						size="icon"
						className={cn("size-8 shrink-0 rounded-full", !alwaysShowBack && "md:hidden")}
						onClick={() => {
							void autosave.flush();
							onBack();
						}}
						aria-label={backLabel}
						title={backLabel}
					>
						<ArrowLeft className="size-4" />
					</Button>
				) : null}
				<span className="flex min-w-0 items-center gap-2 px-1 text-xs text-muted-foreground">
					{current.notebook ? (
						<Link
							href={notebookPath(current.notebook.id)}
							className="min-w-0 rounded-full border px-2.5 py-1 hover:bg-accent hover:text-foreground"
						>
							<NotebookLabel notebook={current.notebook} />
						</Link>
					) : (
						<span className="truncate rounded-full border border-dashed px-2.5 py-1">
							{t("no_notebook")}
						</span>
					)}
				</span>
				<span className="ml-auto flex shrink-0 items-center gap-0.5">
					<NoteSaveStatus
						saveState={autosave.state}
						indexState={current.index_state}
						onRetry={() => void autosave.flush()}
						className="mr-1.5"
					/>
					<Button
						variant="ghost"
						size="icon"
						className={cn("size-8 shrink-0 rounded-full", current.pinned && "text-primary")}
						onClick={() => void togglePin()}
						disabled={pinning}
						aria-pressed={current.pinned}
						aria-label={current.pinned ? t("unpin") : t("pin")}
						title={current.pinned ? t("unpin") : t("pin")}
					>
						<Pin className={cn("size-4", current.pinned && "fill-current")} />
					</Button>
					<NoteSourcesPopover note={current} />
					<NoteMenu note={current} beforeChange={autosave.flush} onDeleted={onDeleted} />
				</span>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto">
				<div
					className={cn(
						"mx-auto w-full max-w-3xl select-text",
						compact ? "px-4 pt-5 pb-16" : "px-5 pt-8 pb-24 sm:px-10 sm:pt-12"
					)}
				>
					<NoteTitleField
						value={title}
						onChange={(next) => {
							setTitle(next);
							autosave.queue({ title: next });
						}}
						onEnter={() =>
							editorRef.current?.querySelector<HTMLElement>("[contenteditable]")?.focus()
						}
						className={
							// "!": globals.css holds a phone's fields at 16px so iOS does
							// not zoom; a title is meant to read as a heading.
							compact ? "text-xl! leading-7" : "text-2xl! leading-8 sm:text-[2rem]! sm:leading-10"
						}
					/>
					<p className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
						<span>{t("edited_ago", { when: relativeTime(current.updated_at, locale) })}</span>
						{fromChat ? (
							<>
								<span aria-hidden>·</span>
								{chatHref ? (
									<Link
										href={chatHref}
										className="inline-flex items-center gap-1 text-primary hover:underline"
									>
										<MessageSquareText className="size-3.5" aria-hidden />
										{t("from_chat_open")}
									</Link>
								) : (
									<span className="inline-flex items-center gap-1">
										<MessageSquareText className="size-3.5" aria-hidden />
										{t("from_chat")}
									</span>
								)}
							</>
						) : null}
						{current.sources.length > 0 ? (
							<>
								<span aria-hidden>·</span>
								<span>
									{t("read_by", {
										notebooks: current.sources.map((source) => source.title).join(", "),
									})}
								</span>
							</>
						) : null}
					</p>
					<div
						ref={editorRef}
						className={cn("min-h-[40vh] text-[15px] leading-7", compact ? "mt-4" : "mt-6")}
					>
						{PlateEditor ? (
							<PlateEditor
								markdown={initialText}
								onMarkdownChange={(markdown) => autosave.queue({ content: markdown })}
								preset="note"
								chatMath
								chatCitations
								defaultEditing
								allowModeToggle={false}
								placeholder={t("text_placeholder")}
								editorVariant="none"
							/>
						) : (
							<div className="space-y-3" aria-busy>
								<Skeleton className="h-4 w-11/12" />
								<Skeleton className="h-4 w-10/12" />
								<Skeleton className="h-4 w-7/12" />
							</div>
						)}
					</div>
				</div>
			</div>
		</article>
	);
}
