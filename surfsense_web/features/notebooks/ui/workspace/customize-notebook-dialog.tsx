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
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { NOTEBOOK_ICONS } from "../../lib/relative-time";
import type { Notebook } from "../../model/notebook";

/** "Customize notebook": its title and emoji. */
export function CustomizeNotebookDialog({
	notebook,
	open,
	onOpenChange,
	onSave,
}: {
	notebook: Notebook;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onSave: (changes: { title?: string; icon?: string }) => Promise<unknown>;
}) {
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="w-[calc(100vw-2rem)] max-w-md select-text">
				{open ? (
					<CustomizeForm notebook={notebook} onClose={() => onOpenChange(false)} onSave={onSave} />
				) : null}
			</DialogContent>
		</Dialog>
	);
}

function CustomizeForm({
	notebook,
	onClose,
	onSave,
}: {
	notebook: Notebook;
	onClose: () => void;
	onSave: (changes: { title?: string; icon?: string }) => Promise<unknown>;
}) {
	const t = useTranslations("notebooks");
	const [initial] = useState(() => ({ title: notebook.title, icon: notebook.icon || "📓" }));
	const [title, setTitle] = useState(initial.title);
	const [icon, setIcon] = useState(initial.icon);
	const [saving, setSaving] = useState(false);
	const [failed, setFailed] = useState(false);
	const showSaving = useDelayedFlag(saving);

	const save = async () => {
		// Only what the user changed: an unchanged title must not overwrite a
		// name the notebook gave itself while the dialog was open, nor mark it
		// as renamed (which stops automatic naming for good).
		const changes: { title?: string; icon?: string } = {};
		if (title.trim() !== initial.title.trim()) changes.title = title.trim();
		if (icon !== initial.icon) changes.icon = icon;
		if (!changes.title && !changes.icon) {
			onClose();
			return;
		}
		setSaving(true);
		setFailed(false);
		try {
			await onSave(changes);
			onClose();
		} catch {
			setFailed(true);
		} finally {
			setSaving(false);
		}
	};

	return (
		<form
			className="flex flex-col gap-4"
			onSubmit={(event) => {
				event.preventDefault();
				if (title.trim()) void save();
			}}
		>
			<DialogHeader>
				<DialogTitle>{t("customize.title")}</DialogTitle>
				<DialogDescription>{t("customize.description")}</DialogDescription>
			</DialogHeader>
			<div className="space-y-2">
				<label htmlFor="notebook-title" className="text-sm font-medium">
					{t("notebook_title")}
				</label>
				<Input
					id="notebook-title"
					value={title}
					maxLength={200}
					onChange={(event) => setTitle(event.target.value)}
				/>
			</div>
			<fieldset className="space-y-2">
				<legend className="text-sm font-medium">{t("customize.emoji")}</legend>
				<div className="grid grid-cols-6 gap-1">
					{NOTEBOOK_ICONS.map((choice) => (
						<button
							key={choice}
							type="button"
							aria-pressed={choice === icon}
							onClick={() => setIcon(choice)}
							className={cn(
								"flex h-10 items-center justify-center rounded-lg border text-xl transition-colors hover:bg-accent",
								choice === icon ? "border-primary bg-primary/5" : "border-transparent"
							)}
						>
							{choice}
						</button>
					))}
				</div>
			</fieldset>
			{failed ? (
				<p className="text-sm text-destructive" role="alert">
					{t("rename_failed")}
				</p>
			) : null}
			<div className="flex justify-end gap-2">
				<Button type="button" variant="ghost" onClick={onClose} disabled={saving}>
					{t("cancel")}
				</Button>
				<Button type="submit" disabled={saving || !title.trim()}>
					{showSaving ? t("saving") : t("save")}
				</Button>
			</div>
		</form>
	);
}
