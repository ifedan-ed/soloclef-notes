"use client";

import { ArrowLeft, Folder } from "lucide-react";
import { useTranslations } from "next-intl";
import { useId, useState } from "react";
import { getDocumentTypeIcon } from "@/components/documents/DocumentTypeIcon";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DialogTitle } from "@/components/ui/dialog";
import type { NotebookSource } from "../../model/notebook";

/** The dialog's "Sources" sub-view: tick the sources this one output uses. */
export function SourcePickerView({
	sources,
	selected,
	onConfirm,
	onBack,
}: {
	sources: NotebookSource[];
	selected: number[];
	onConfirm: (ids: number[]) => void;
	onBack: () => void;
}) {
	const t = useTranslations("notebooks");
	const [draft, setDraft] = useState(() => new Set(selected));
	const idPrefix = useId();
	const allSelected = sources.length > 0 && sources.every((source) => draft.has(source.id));

	const toggle = (id: number, on: boolean) =>
		setDraft((current) => {
			const next = new Set(current);
			if (on) next.add(id);
			else next.delete(id);
			return next;
		});

	return (
		<div className="flex min-h-0 flex-col gap-3">
			<div className="flex items-center gap-2">
				<Button
					variant="ghost"
					size="icon"
					className="size-8 rounded-full"
					onClick={onBack}
					aria-label={t("back")}
				>
					<ArrowLeft className="size-4" />
				</Button>
				{/* The dialog's title while this view replaces the options view. */}
				<DialogTitle className="text-base font-semibold">{t("sources")}</DialogTitle>
			</div>
			<div className="flex items-center gap-2 rounded-lg px-2 py-1 text-sm text-muted-foreground">
				<label htmlFor={`${idPrefix}-all`} className="flex-1 cursor-pointer">
					{t("select_all")}
				</label>
				<Checkbox
					id={`${idPrefix}-all`}
					checked={allSelected}
					onCheckedChange={(value) =>
						setDraft(value === true ? new Set(sources.map((s) => s.id)) : new Set())
					}
				/>
			</div>
			<div className="max-h-80 min-h-0 overflow-y-auto">
				{sources.map((source) => (
					<div
						key={source.id}
						className="flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-accent"
					>
						<span className="flex size-5 shrink-0 items-center justify-center text-muted-foreground">
							{source.kind === "folder" ? (
								<Folder className="size-4" />
							) : (
								getDocumentTypeIcon(source.document_type ?? "FILE", "size-4")
							)}
						</span>
						<label
							htmlFor={`${idPrefix}-${source.id}`}
							className="min-w-0 flex-1 cursor-pointer truncate text-sm"
						>
							{source.title}
						</label>
						<Checkbox
							id={`${idPrefix}-${source.id}`}
							checked={draft.has(source.id)}
							onCheckedChange={(value) => toggle(source.id, value === true)}
						/>
					</div>
				))}
			</div>
			<div className="flex justify-end">
				<Button disabled={draft.size === 0} onClick={() => onConfirm([...draft])}>
					{t("confirm")}
				</Button>
			</div>
		</div>
	);
}
