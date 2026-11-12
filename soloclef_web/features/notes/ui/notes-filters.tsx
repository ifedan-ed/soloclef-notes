"use client";

import { ArrowDownUp, Check, ChevronDown, X } from "lucide-react";
import { useTranslations } from "next-intl";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { Notebook } from "@/features/notebooks/model/notebook";
import { cn } from "@/lib/utils";
import type { NotesFilter, NotesSort } from "../lib/notes-filter";

/** The filter chips: everything a list can be narrowed by but the search. */
export function NotesFilters({
	filter,
	onChange,
	notebooks,
}: {
	filter: NotesFilter;
	onChange: (filter: NotesFilter) => void;
	notebooks: Notebook[];
}) {
	const t = useTranslations("notes");
	const chosenNotebook =
		typeof filter.notebook === "number"
			? notebooks.find((notebook) => notebook.id === filter.notebook)
			: undefined;
	const nothingChosen = !filter.pinned && !filter.origin && filter.notebook === undefined;
	const set = (changes: Partial<NotesFilter>) => onChange({ ...filter, ...changes });

	return (
		<div className="flex min-w-0 flex-wrap items-center gap-1.5">
			<Chip
				active={nothingChosen}
				onClick={() => onChange({ query: filter.query, sort: filter.sort })}
			>
				{t("filter_all")}
			</Chip>
			<Chip active={!!filter.pinned} onClick={() => set({ pinned: !filter.pinned })}>
				{t("filter_pinned")}
			</Chip>
			<Chip
				active={filter.origin === "chat"}
				onClick={() => set({ origin: filter.origin === "chat" ? undefined : "chat" })}
			>
				{t("filter_from_chats")}
			</Chip>
			<Chip
				active={filter.notebook === "none"}
				onClick={() => set({ notebook: filter.notebook === "none" ? undefined : "none" })}
			>
				{t("filter_no_notebook")}
			</Chip>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<button type="button" className={chipClass(!!chosenNotebook)}>
						<span className="max-w-36 truncate">
							{chosenNotebook
								? `${chosenNotebook.icon ? `${chosenNotebook.icon} ` : ""}${chosenNotebook.title}`
								: t("filter_notebook")}
						</span>
						{chosenNotebook ? (
							<X
								className="size-3.5"
								aria-label={t("filter_clear_notebook")}
								onPointerDown={(event) => {
									// Clears instead of opening the menu.
									event.preventDefault();
									event.stopPropagation();
									set({ notebook: undefined });
								}}
							/>
						) : (
							<ChevronDown className="size-3.5" aria-hidden />
						)}
					</button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="start" className="max-h-80 w-64 overflow-y-auto">
					<DropdownMenuLabel>{t("filter_notebook_label")}</DropdownMenuLabel>
					{notebooks.length === 0 ? (
						<p className="px-2 py-1.5 text-sm text-muted-foreground">{t("no_notebooks")}</p>
					) : (
						notebooks.map((notebook) => (
							<DropdownMenuItem key={notebook.id} onSelect={() => set({ notebook: notebook.id })}>
								<span aria-hidden className="w-5 shrink-0 text-center">
									{notebook.icon ?? "📓"}
								</span>
								<span className="min-w-0 flex-1 truncate">{notebook.title}</span>
								{filter.notebook === notebook.id ? <Check className="size-4" /> : null}
							</DropdownMenuItem>
						))
					)}
				</DropdownMenuContent>
			</DropdownMenu>
		</div>
	);
}

/** How the list is ordered after its pinned notes. */
export function NotesSortMenu({
	sort,
	onChange,
}: {
	sort: NotesSort | undefined;
	onChange: (sort: NotesSort) => void;
}) {
	const t = useTranslations("notes");
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button
					variant="ghost"
					size="icon"
					className="size-8 shrink-0 rounded-full"
					aria-label={t("sort_label")}
					title={t("sort_label")}
				>
					<ArrowDownUp className="size-4" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-52">
				<DropdownMenuLabel>{t("sort_label")}</DropdownMenuLabel>
				<DropdownMenuRadioGroup
					value={sort ?? "edited"}
					onValueChange={(value) => onChange(value as NotesSort)}
				>
					<DropdownMenuRadioItem value="edited">{t("sort_edited")}</DropdownMenuRadioItem>
					<DropdownMenuRadioItem value="created">{t("sort_created")}</DropdownMenuRadioItem>
					<DropdownMenuRadioItem value="title">{t("sort_title")}</DropdownMenuRadioItem>
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function chipClass(active: boolean) {
	return cn(
		"inline-flex h-7 shrink-0 items-center gap-1 rounded-full border px-3 text-xs font-medium transition-colors",
		"focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
		active
			? "border-primary bg-primary text-primary-foreground"
			: "border-border bg-background text-muted-foreground hover:bg-accent hover:text-foreground"
	);
}

function Chip({
	active,
	onClick,
	children,
}: {
	active: boolean;
	onClick: () => void;
	children: ReactNode;
}) {
	return (
		<button type="button" aria-pressed={active} onClick={onClick} className={chipClass(active)}>
			{children}
		</button>
	);
}
