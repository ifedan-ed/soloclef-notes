"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { defaultNotebookIcon } from "@/features/notebooks/lib/relative-time";
import type { Notebook } from "@/features/notebooks/model/notebook";

interface NotebookPickerDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	title: string;
	notebooks: Notebook[];
	onPick: (notebook: Notebook) => Promise<unknown>;
}

/** Choose one notebook of the workspace; closes once the action succeeds. */
export function NotebookPickerDialog({
	open,
	onOpenChange,
	title,
	notebooks,
	onPick,
}: NotebookPickerDialogProps) {
	const t = useTranslations("notes");
	const [pending, setPending] = useState<number | null>(null);

	const pick = async (notebook: Notebook) => {
		setPending(notebook.id);
		try {
			await onPick(notebook);
			onOpenChange(false);
		} catch {
			// The caller says what went wrong; the dialog stays open to retry.
		} finally {
			setPending(null);
		}
	};

	return (
		<Dialog open={open} onOpenChange={(next) => pending === null && onOpenChange(next)}>
			<DialogContent className="flex max-h-[80vh] w-[calc(100vw-2rem)] max-w-md flex-col gap-3">
				<DialogHeader>
					<DialogTitle>{title}</DialogTitle>
					<DialogDescription className="sr-only">{title}</DialogDescription>
				</DialogHeader>
				{notebooks.length === 0 ? (
					<p className="text-sm text-muted-foreground">{t("no_other_notebooks")}</p>
				) : (
					<ul className="-mx-2 min-h-0 flex-1 space-y-0.5 overflow-y-auto">
						{notebooks.map((notebook) => (
							<li key={notebook.id}>
								<Button
									variant="ghost"
									className="h-auto w-full justify-start gap-3 px-2 py-2 text-left font-normal"
									disabled={pending !== null}
									onClick={() => void pick(notebook)}
								>
									<span className="text-lg leading-none" aria-hidden>
										{notebook.icon || defaultNotebookIcon(notebook.id)}
									</span>
									<span className="min-w-0 flex-1 truncate">{notebook.title}</span>
									{pending === notebook.id ? <Spinner size="sm" /> : null}
								</Button>
							</li>
						))}
					</ul>
				)}
			</DialogContent>
		</Dialog>
	);
}
