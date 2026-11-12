"use client";

import { Plus, RefreshCw, Search, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { useNotebooks } from "@/features/notebooks/hooks/use-notebook-queries";
import { cn } from "@/lib/utils";
import { useNotesList } from "../hooks/use-notes-list";
import { isFiltered, type NotesFilter, searchText } from "../lib/notes-filter";
import { NoteListRow } from "./note-list-row";
import { NotesEmpty } from "./notes-empty";
import { NotesFilters, NotesSortMenu } from "./notes-filters";

const SKELETON_KEYS = ["a", "b", "c", "d", "e"];

interface NotesListPaneProps {
	filter: NotesFilter;
	onFilterChange: (filter: NotesFilter) => void;
	selectedId: number | null;
	onOpen: (noteId: number) => void;
	onNew: () => void;
	creating: boolean;
	className?: string;
}

/** The left pane of the Notes place: the title, New note, the search, the filters and the list. */
export function NotesListPane({
	filter,
	onFilterChange,
	selectedId,
	onOpen,
	onNew,
	creating,
	className,
}: NotesListPaneProps) {
	const t = useTranslations("notes");
	const { data: notebooks = [] } = useNotebooks();
	const list = useNotesList(filter);
	const showSkeleton = useDelayedFlag(list.isLoading);
	const notes = list.data?.pages.flatMap((page) => page.items) ?? [];
	const total = list.data?.pages[0]?.total ?? 0;
	const searching = list.isFetching && !list.isFetchingNextPage && !list.isLoading;

	return (
		<div className={cn("flex min-h-0 min-w-0 flex-col", className)}>
			<header className="space-y-3 px-4 pt-5 pb-3">
				<div className="flex items-center gap-2">
					<h1 className="text-xl font-semibold tracking-tight">{t("title")}</h1>
					{list.data && !isFiltered(filter) && total > 0 ? (
						<span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium tabular-nums text-muted-foreground">
							{total}
						</span>
					) : null}
					<Button
						size="sm"
						className="ml-auto h-8 rounded-full"
						onClick={onNew}
						disabled={creating}
					>
						{creating ? <Spinner size="xs" /> : <Plus className="size-4" />}
						{t("new_note")}
					</Button>
				</div>
				<div className="flex items-center gap-1">
					<div className="relative min-w-0 flex-1">
						<Search
							className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
							aria-hidden
						/>
						<Input
							value={filter.query ?? ""}
							onChange={(event) => onFilterChange({ ...filter, query: event.target.value })}
							placeholder={t("search")}
							aria-label={t("search")}
							className="h-9 rounded-full bg-background pr-9 pl-9 [&::-webkit-search-cancel-button]:hidden"
							type="search"
						/>
						{searching ? (
							<Spinner size="xs" className="absolute right-3 top-1/2 -translate-y-1/2" />
						) : filter.query ? (
							<button
								type="button"
								className="absolute right-2 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded-full text-muted-foreground hover:bg-accent"
								onClick={() => onFilterChange({ ...filter, query: "" })}
								aria-label={t("clear_search")}
							>
								<X className="size-3.5" />
							</button>
						) : null}
					</div>
					<NotesSortMenu
						sort={filter.sort}
						onChange={(sort) => onFilterChange({ ...filter, sort })}
					/>
				</div>
				<NotesFilters filter={filter} onChange={onFilterChange} notebooks={notebooks} />
			</header>
			<div className="min-h-0 flex-1 overflow-y-auto px-2 pb-4">
				{list.isError && !list.data ? (
					<div className="m-2 flex items-center gap-3 rounded-xl border p-3 text-sm">
						<span className="flex-1 text-muted-foreground">{t("load_failed")}</span>
						<Button size="sm" variant="outline" onClick={() => void list.refetch()}>
							<RefreshCw className="size-4" />
							{t("retry")}
						</Button>
					</div>
				) : list.isLoading ? (
					showSkeleton ? (
						<div className="space-y-2 px-1" aria-busy>
							{SKELETON_KEYS.map((key) => (
								<Skeleton key={key} className="h-[4.5rem] w-full rounded-xl" />
							))}
						</div>
					) : null
				) : notes.length === 0 ? (
					<NotesEmpty
						filtered={isFiltered(filter)}
						query={searchText(filter.query)}
						onNew={onNew}
						onClear={() => onFilterChange({ sort: filter.sort })}
						creating={creating}
					/>
				) : (
					<ul className="space-y-0.5" aria-label={t("title")}>
						{notes.map((note) => (
							<li key={note.id}>
								<NoteListRow
									note={note}
									selected={note.id === selectedId}
									onOpen={() => onOpen(note.id)}
									showNotebook={typeof filter.notebook !== "number"}
								/>
							</li>
						))}
						{list.hasNextPage ? (
							<li className="flex justify-center pt-2">
								<Button
									size="sm"
									variant="ghost"
									className="rounded-full"
									disabled={list.isFetchingNextPage}
									onClick={() => void list.fetchNextPage()}
								>
									{list.isFetchingNextPage ? <Spinner size="xs" /> : null}
									{t("show_more", { count: total - notes.length })}
								</Button>
							</li>
						) : null}
					</ul>
				)}
			</div>
		</div>
	);
}
