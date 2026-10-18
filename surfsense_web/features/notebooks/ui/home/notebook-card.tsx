"use client";

import { Check, Copy, MoreVertical, Pencil, Pin, PinOff, Trash2, X } from "lucide-react";
import Link from "next/link";
import { useLocale, useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { notebookCover } from "../../lib/notebook-cover";
import { formatNotebookDate } from "../../lib/notebook-dates";
import type { Notebook } from "../../model/notebook";

export type CardVariant = "grid" | "list" | "featured";

export interface NotebookCardActions {
	onRename: (title: string) => Promise<unknown>;
	onDelete: () => Promise<unknown>;
	onToggleFeatured: () => Promise<unknown>;
	onCopy: () => Promise<unknown>;
}

type Mode = "idle" | "renaming" | "confirming-delete";

/** One notebook on the home: a grid card, a list row or a large featured card. */
export function NotebookCard({
	notebook,
	href,
	variant,
	...actions
}: NotebookCardActions & { notebook: Notebook; href: string; variant: CardVariant }) {
	const t = useTranslations("notebooks");
	const locale = useLocale();
	const [mode, setMode] = useState<Mode>("idle");
	const [draft, setDraft] = useState(notebook.title);
	const [busy, setBusy] = useState(false);

	const run = async (action: () => Promise<unknown>) => {
		setBusy(true);
		try {
			await action();
			setMode("idle");
		} finally {
			setBusy(false);
		}
	};

	const meta = `${formatNotebookDate(notebook.updated_at, locale)} · ${t("source_count", {
		count: notebook.source_count,
	})}`;
	const icon = notebook.icon || "📓";

	const menu = (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button
					variant="ghost"
					size="icon"
					className={cn(
						"size-8 shrink-0 rounded-full text-muted-foreground",
						variant !== "list" && "absolute right-2 top-2 bg-background/60 backdrop-blur-sm"
					)}
					aria-label={t("more_actions")}
				>
					<MoreVertical className="size-4" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end">
				<DropdownMenuItem
					onSelect={() => {
						setDraft(notebook.title);
						setMode("renaming");
					}}
				>
					<Pencil className="size-4" />
					{t("rename")}
				</DropdownMenuItem>
				<DropdownMenuItem onSelect={() => void actions.onToggleFeatured()}>
					{notebook.featured ? <PinOff className="size-4" /> : <Pin className="size-4" />}
					{notebook.featured ? t("unpin_featured") : t("pin_featured")}
				</DropdownMenuItem>
				<DropdownMenuItem onSelect={() => void actions.onCopy()}>
					<Copy className="size-4" />
					{t("copy_notebook")}
				</DropdownMenuItem>
				<DropdownMenuSeparator />
				<DropdownMenuItem
					className="text-destructive focus:text-destructive"
					onSelect={() => setMode("confirming-delete")}
				>
					<Trash2 className="size-4" />
					{t("delete")}
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);

	const shell = cn(
		"group relative flex min-w-0 rounded-2xl border bg-muted/30 transition-colors",
		mode === "idle" && "hover:bg-accent",
		variant === "list" ? "items-center gap-3 px-3 py-2" : "flex-col"
	);

	if (mode === "renaming") {
		return (
			<form
				className={cn(shell, "gap-3 p-4")}
				onSubmit={(event) => {
					event.preventDefault();
					const title = draft.trim();
					if (title) void run(() => actions.onRename(title));
				}}
			>
				<Input
					autoFocus
					value={draft}
					maxLength={200}
					onChange={(event) => setDraft(event.target.value)}
					onKeyDown={(event) => {
						if (event.key === "Escape") setMode("idle");
					}}
					aria-label={t("rename")}
					className="h-8"
				/>
				<div className="flex justify-end gap-2">
					<Button type="button" size="sm" variant="ghost" onClick={() => setMode("idle")}>
						<X className="size-4" />
						{t("cancel")}
					</Button>
					<Button type="submit" size="sm" disabled={busy || !draft.trim()}>
						<Check className="size-4" />
						{t("save")}
					</Button>
				</div>
			</form>
		);
	}

	if (mode === "confirming-delete") {
		return (
			<div className={cn(shell, "gap-3 p-4")} role="alert">
				<p className="text-sm font-medium text-foreground">
					{t("delete_confirm_title", { title: notebook.title })}
				</p>
				<p className="text-xs text-muted-foreground">{t("delete_confirm_body")}</p>
				<div className="flex justify-end gap-2">
					<Button size="sm" variant="ghost" onClick={() => setMode("idle")} disabled={busy}>
						{t("cancel")}
					</Button>
					<Button
						size="sm"
						variant="destructive"
						onClick={() => run(actions.onDelete)}
						disabled={busy}
					>
						{t("delete")}
					</Button>
				</div>
			</div>
		);
	}

	if (variant === "list") {
		return (
			<div className={shell}>
				<Link
					href={href}
					className="flex min-w-0 flex-1 items-center gap-3 rounded-xl py-1 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					<span className="text-2xl leading-none" aria-hidden>
						{icon}
					</span>
					<span className="min-w-0 flex-1 truncate text-sm font-medium">{notebook.title}</span>
					<span className="hidden shrink-0 text-xs text-muted-foreground sm:inline">{meta}</span>
				</Link>
				{menu}
			</div>
		);
	}

	if (variant === "featured") {
		return (
			<div className={cn(shell, "overflow-hidden")}>
				<Link
					href={href}
					className="flex flex-1 flex-col focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					<span
						className={cn(
							"flex h-28 items-center justify-center text-5xl",
							notebookCover(notebook.id)
						)}
						aria-hidden
					>
						{icon}
					</span>
					<span className="flex flex-col gap-1 p-4">
						<span className="line-clamp-2 text-base font-semibold leading-snug">
							{notebook.title}
						</span>
						<span className="truncate text-xs text-muted-foreground">{meta}</span>
					</span>
				</Link>
				{menu}
			</div>
		);
	}

	return (
		<div className={cn(shell, "min-h-40")}>
			<Link
				href={href}
				className="flex min-h-40 flex-1 flex-col gap-3 rounded-2xl p-4 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
			>
				<span className="text-3xl leading-none" aria-hidden>
					{icon}
				</span>
				<span className="line-clamp-2 pr-6 text-sm font-medium leading-5 text-foreground">
					{notebook.title}
				</span>
				<span className="mt-auto truncate text-xs text-muted-foreground">{meta}</span>
			</Link>
			{menu}
		</div>
	);
}
