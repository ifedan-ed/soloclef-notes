"use client";

import {
	Copy,
	MessageSquareX,
	MoreVertical,
	Palette,
	Pin,
	PinOff,
	SlidersHorizontal,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { NotebookChanges } from "../../api/notebooks-api";
import type { Notebook } from "../../model/notebook";
import { ConfigureChatDialog } from "./configure-chat-dialog";
import { CustomizeNotebookDialog } from "./customize-notebook-dialog";
import { DeleteHistoryDialog } from "./delete-history-dialog";

type Open = "configure" | "customize" | "delete-history" | null;

/**
 * The notebook's ⋮ menu and the dialogs it opens. It has no Share item on
 * purpose: only a Studio output can be shared, from that output's own view.
 */
export function NotebookMenu({
	notebook,
	onUpdate,
	onCopy,
	onSetFeatured,
	onDeleteHistory,
}: {
	notebook: Notebook;
	onUpdate: (changes: NotebookChanges) => Promise<unknown>;
	onCopy: () => Promise<unknown>;
	onSetFeatured: (featured: boolean) => Promise<unknown>;
	onDeleteHistory: () => Promise<unknown>;
}) {
	const t = useTranslations("notebooks");
	const [open, setOpen] = useState<Open>(null);
	const [copying, setCopying] = useState(false);
	const close = (next: boolean) => {
		if (!next) setOpen(null);
	};

	const copy = async () => {
		if (copying) return;
		setCopying(true);
		try {
			await onCopy();
			toast.success(t("copied"));
		} catch {
			toast.error(t("copy_failed"));
		} finally {
			setCopying(false);
		}
	};

	const toggleFeatured = async () => {
		try {
			await onSetFeatured(!notebook.featured);
		} catch {
			toast.error(t("feature_failed"));
		}
	};

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						variant="ghost"
						size="icon"
						className="size-9 shrink-0 rounded-full"
						aria-label={t("notebook_menu")}
					>
						<MoreVertical className="size-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end" className="w-60">
					<DropdownMenuItem onSelect={() => setOpen("configure")}>
						<SlidersHorizontal className="size-4" />
						{t("configure_chat.title")}
					</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => setOpen("customize")}>
						<Palette className="size-4" />
						{t("customize.title")}
					</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => void copy()} disabled={copying}>
						<Copy className="size-4" />
						{t("copy_notebook")}
					</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => void toggleFeatured()}>
						{notebook.featured ? <PinOff className="size-4" /> : <Pin className="size-4" />}
						{notebook.featured ? t("unpin_featured") : t("pin_featured")}
					</DropdownMenuItem>
					<DropdownMenuSeparator />
					<DropdownMenuItem
						className="text-destructive focus:text-destructive"
						onSelect={() => setOpen("delete-history")}
					>
						<MessageSquareX className="size-4" />
						{t("delete_history.title")}
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			<ConfigureChatDialog
				notebook={notebook}
				open={open === "configure"}
				onOpenChange={close}
				onSave={onUpdate}
			/>
			<CustomizeNotebookDialog
				notebook={notebook}
				open={open === "customize"}
				onOpenChange={close}
				onSave={onUpdate}
			/>
			<DeleteHistoryDialog
				open={open === "delete-history"}
				onOpenChange={close}
				onConfirm={onDeleteHistory}
			/>
		</>
	);
}
