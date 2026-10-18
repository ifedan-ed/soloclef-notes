"use client";

import { ArrowLeft, Plus } from "lucide-react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { Logo } from "@/components/Logo";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Spinner } from "@/components/ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { BRAND } from "@/lib/brand";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { NOTEBOOK_ICONS } from "../../lib/relative-time";
import type { Notebook } from "../../model/notebook";

/**
 * The notebook's own top bar (the app sidebars are hidden on this page):
 * a way home, the editable emoji and title, "Create notebook" and the menu.
 */
export function NotebookHeader({
	notebook,
	homeHref,
	onRename,
	onIconChange,
	onCreate,
	creating,
	menu,
}: {
	notebook: Notebook | undefined;
	homeHref: string;
	onRename: (title: string) => void;
	onIconChange: (icon: string) => void;
	onCreate: () => void;
	creating: boolean;
	menu: ReactNode;
}) {
	const t = useTranslations("notebooks");
	const [title, setTitle] = useState(notebook?.title ?? "");
	const [iconOpen, setIconOpen] = useState(false);
	const showCreating = useDelayedFlag(creating);
	// Follow a new title (an automatic name arriving, a rename elsewhere)
	// unless the user is mid-edit: their typing is never overwritten.
	const shown = useRef(notebook?.title ?? "");
	useEffect(() => {
		const next = notebook?.title ?? "";
		setTitle((current) => (current === shown.current ? next : current));
		shown.current = next;
	}, [notebook?.title]);

	const commit = () => {
		if (!notebook) return;
		const next = title.trim();
		if (!next) {
			setTitle(notebook.title);
			return;
		}
		if (next !== notebook.title) onRename(next);
	};

	return (
		// The page is laid out edge to edge (viewport-fit=cover), so on an iPhone
		// the header ran up under the status bar: it clears the top inset itself.
		<header className="flex h-[calc(3.5rem+env(safe-area-inset-top))] shrink-0 items-center gap-1.5 border-b px-2 pt-[env(safe-area-inset-top)] sm:gap-2 sm:px-3">
			<Tooltip>
				<TooltipTrigger asChild>
					<Button asChild variant="ghost" size="icon" className="size-9 shrink-0 rounded-full">
						<Link href={homeHref} aria-label={t("back_to_notebooks")}>
							<ArrowLeft className="size-4" />
						</Link>
					</Button>
				</TooltipTrigger>
				<TooltipContent>{t("back_to_notebooks")}</TooltipContent>
			</Tooltip>
			<Link
				href={homeHref}
				aria-label={t("home")}
				className="hidden shrink-0 items-center gap-2 rounded-lg px-1.5 py-1 hover:bg-accent sm:flex"
			>
				<Logo disableLink className="size-6 rounded-md" />
				<span className="text-sm font-semibold">{BRAND.name}</span>
			</Link>
			<span className="mx-1 hidden h-5 w-px bg-border sm:block" aria-hidden />
			{notebook ? (
				<>
					<Popover open={iconOpen} onOpenChange={setIconOpen}>
						<PopoverTrigger asChild>
							<button
								type="button"
								className="flex size-9 shrink-0 items-center justify-center rounded-lg text-xl hover:bg-accent"
								aria-label={t("change_icon")}
							>
								{notebook.icon || "📓"}
							</button>
						</PopoverTrigger>
						<PopoverContent align="start" className="grid w-auto grid-cols-6 gap-1 p-2">
							{NOTEBOOK_ICONS.map((icon) => (
								<button
									key={icon}
									type="button"
									className="flex size-9 items-center justify-center rounded-lg text-xl hover:bg-accent"
									onClick={() => {
										onIconChange(icon);
										setIconOpen(false);
									}}
								>
									{icon}
								</button>
							))}
						</PopoverContent>
					</Popover>
					<input
						value={title}
						onChange={(event) => setTitle(event.target.value)}
						onBlur={commit}
						onKeyDown={(event) => {
							if (event.key === "Enter") event.currentTarget.blur();
							if (event.key === "Escape") {
								setTitle(notebook.title);
								event.currentTarget.blur();
							}
						}}
						maxLength={200}
						aria-label={t("notebook_title")}
						className="min-w-0 flex-1 truncate rounded-md border border-transparent bg-transparent px-2 py-1 text-base font-semibold outline-none hover:border-input focus:border-input"
					/>
				</>
			) : (
				<div className="flex-1" />
			)}
			<Button
				variant="outline"
				className="shrink-0 rounded-full"
				onClick={onCreate}
				disabled={creating}
				aria-label={t("create")}
			>
				{showCreating ? <Spinner size="sm" /> : <Plus className="size-4" />}
				<span className="hidden md:inline">{t("create")}</span>
			</Button>
			{menu}
		</header>
	);
}
