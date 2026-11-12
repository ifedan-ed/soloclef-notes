"use client";

import { Check, FolderInput, FolderMinus, MoreHorizontal, Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useNotebooks } from "@/features/notebooks/hooks/use-notebook-queries";
import { useNoteActions } from "../hooks/use-note-actions";
import type { Note } from "../model/note";

/** The note's other actions: file it in a notebook or take it out, and delete it. */
export function NoteMenu({
	note,
	beforeChange,
	onDeleted,
}: {
	note: Note;
	/** Saves what is typed, so a move or a delete acts on the note as it is on screen. */
	beforeChange: () => Promise<void>;
	onDeleted: () => void;
}) {
	const t = useTranslations("notes");
	const { data: notebooks = [] } = useNotebooks();
	const actions = useNoteActions();
	const [confirming, setConfirming] = useState(false);
	const [deleting, setDeleting] = useState(false);

	const file = async (notebookId: number | null) => {
		try {
			await beforeChange();
			await actions.file(note.id, notebookId);
			const target = notebooks.find((notebook) => notebook.id === notebookId);
			toast.success(target ? t("moved_to", { notebook: target.title }) : t("moved_out"));
		} catch {
			toast.error(t("action_failed"));
		}
	};
	const remove = async () => {
		setDeleting(true);
		try {
			await beforeChange();
			await actions.remove(note);
			toast.success(t("deleted"));
			onDeleted();
		} catch {
			toast.error(t("delete_failed"));
		} finally {
			setDeleting(false);
			setConfirming(false);
		}
	};

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						variant="ghost"
						size="icon"
						className="size-8 shrink-0 rounded-full"
						aria-label={t("more_actions")}
					>
						<MoreHorizontal className="size-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end" className="w-60">
					<DropdownMenuSub>
						<DropdownMenuSubTrigger>
							<FolderInput className="size-4" />
							{note.notebook ? t("move_to_notebook") : t("file_in_notebook")}
						</DropdownMenuSubTrigger>
						<DropdownMenuSubContent className="max-h-80 w-60 overflow-y-auto">
							{notebooks.length === 0 ? (
								<p className="px-2 py-1.5 text-sm text-muted-foreground">{t("no_notebooks")}</p>
							) : (
								notebooks.map((notebook) => (
									<DropdownMenuItem
										key={notebook.id}
										disabled={notebook.id === note.notebook?.id}
										onSelect={() => void file(notebook.id)}
									>
										<span aria-hidden className="w-5 shrink-0 text-center">
											{notebook.icon ?? "📓"}
										</span>
										<span className="min-w-0 flex-1 truncate">{notebook.title}</span>
										{notebook.id === note.notebook?.id ? <Check className="size-4" /> : null}
									</DropdownMenuItem>
								))
							)}
						</DropdownMenuSubContent>
					</DropdownMenuSub>
					{note.notebook ? (
						<DropdownMenuItem onSelect={() => void file(null)}>
							<FolderMinus className="size-4" />
							{t("take_out_of_notebook")}
						</DropdownMenuItem>
					) : null}
					<DropdownMenuSeparator />
					<DropdownMenuItem
						className="text-destructive focus:text-destructive"
						onSelect={() => setConfirming(true)}
					>
						<Trash2 className="size-4" />
						{t("delete")}
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			<AlertDialog open={confirming} onOpenChange={setConfirming}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>{t("delete_title")}</AlertDialogTitle>
						<AlertDialogDescription>
							{t("delete_body", { title: note.title })}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel disabled={deleting}>{t("cancel")}</AlertDialogCancel>
						<AlertDialogAction
							className="bg-destructive text-white hover:bg-destructive/90"
							disabled={deleting}
							onClick={(event) => {
								event.preventDefault();
								void remove();
							}}
						>
							{t("delete")}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	);
}
