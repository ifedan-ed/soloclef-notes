"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { LayoutGrid, List, Plus, RefreshCw, Search, Settings } from "lucide-react";
import Link from "next/link";
import { useTranslations } from "next-intl";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { Logo } from "@/components/Logo";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Spinner } from "@/components/ui/spinner";
import { workspacesApiService } from "@/lib/apis/workspaces-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { cn } from "@/lib/utils";
import { notebookKeys } from "../../api/notebook-query-keys";
import { notebooksApi } from "../../api/notebooks-api";
import { useContentSearch } from "../../hooks/use-content-search";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { useHomeView } from "../../hooks/use-home-view";
import {
	useCopyNotebook,
	useCreateNotebook,
	useSetFeatured,
	useUpdateNotebook,
} from "../../hooks/use-notebook-actions";
import { useNotebooks } from "../../hooks/use-notebook-queries";
import { type HomeTab, homeSections } from "../../lib/home-filter";
import type { Notebook } from "../../model/notebook";
import { ContentSearchResults } from "./content-search-results";
import { NotebookCard } from "./notebook-card";

const SKELETON_KEYS = ["a", "b", "c", "d", "e", "f"];

/** The notebooks home: what a workspace opens on. */
export function NotebooksHome({ workspaceId }: { workspaceId: number }) {
	const t = useTranslations("notebooks");
	const queryClient = useQueryClient();
	const { data: notebooks, isLoading, error, refetch } = useNotebooks(workspaceId);
	// The workspace's name heads the page: with no second sidebar, this is
	// where it shows (the shell has already fetched it, so this is the cache).
	const { data: workspace } = useQuery({
		queryKey: cacheKeys.workspaces.detail(String(workspaceId)),
		queryFn: () => workspacesApiService.getWorkspace({ id: workspaceId }),
		enabled: Number.isFinite(workspaceId) && workspaceId > 0,
	});
	const showSkeleton = useDelayedFlag(isLoading);
	const [tab, setTab] = useState<HomeTab>("all");
	const [query, setQuery] = useState("");
	const [view, setView] = useHomeView();
	const { create, creating } = useCreateNotebook(workspaceId);
	const showCreating = useDelayedFlag(creating);
	const update = useUpdateNotebook(workspaceId);
	const setFeatured = useSetFeatured(workspaceId);
	const copy = useCopyNotebook(workspaceId);
	const inside = useContentSearch(workspaceId, query);
	const showInsideSpinner = useDelayedFlag(inside.pending);
	const searchRef = useRef<HTMLInputElement>(null);
	const insideListRef = useRef<HTMLDivElement>(null);

	const href = (id: number) => `/dashboard/${workspaceId}/notebooks/${id}`;
	const sections = homeSections(notebooks ?? [], tab, query);

	const onCreate = () => void create().catch(() => toast.error(t("create_failed")));
	const actionsFor = (notebook: Notebook) => ({
		onRename: (title: string) =>
			update(notebook.id, { title }).catch(() => toast.error(t("rename_failed"))),
		onToggleFeatured: () =>
			setFeatured(notebook.id, !notebook.featured).catch(() => toast.error(t("feature_failed"))),
		onCopy: () => copy(notebook).catch(() => toast.error(t("copy_failed"))),
		onDelete: async () => {
			try {
				await notebooksApi.remove(workspaceId, notebook.id);
				queryClient.setQueryData<Notebook[]>(notebookKeys.list(workspaceId), (current = []) =>
					current.filter((item) => item.id !== notebook.id)
				);
			} catch {
				toast.error(t("delete_failed"));
			}
		},
	});

	const headerLinks = (
		<>
			<Button asChild variant="ghost" size="icon" className="size-9 shrink-0 rounded-full">
				<Link href={`/dashboard/${workspaceId}/user-settings`} aria-label={t("settings")}>
					<Settings className="size-4" />
				</Link>
			</Button>
		</>
	);

	return (
		<div className="w-full min-w-0 max-w-full space-y-8 overflow-x-hidden">
			{/* Two rows on a phone (title + links, then filter + search), one row from sm up.
			    On one wrapping row the search box shrank under the Recent tab. */}
			<header className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center">
				<div className="flex items-center gap-2">
					<Logo disableLink className="size-7 rounded-md" />
					<div className="min-w-0">
						{workspace?.name ? (
							<p
								className="max-w-[16rem] truncate text-xs leading-tight text-muted-foreground"
								title={workspace.name}
							>
								{workspace.name}
							</p>
						) : null}
						<h1 className="text-xl font-semibold leading-tight text-foreground">
							{t("home_title")}
						</h1>
					</div>
					<div className="ml-auto flex items-center gap-1 sm:hidden">{headerLinks}</div>
				</div>
				<div className="flex min-w-0 items-center gap-2 sm:contents">
					<SegmentedControl
						ariaLabel={t("home_filter")}
						value={tab}
						onValueChange={(value) => setTab(value as HomeTab)}
						options={[
							{ value: "all", label: t("tab_all") },
							{ value: "recent", label: t("tab_recent") },
						]}
					/>
					<div className="relative min-w-[8rem] flex-1 sm:w-64 sm:flex-none">
						<Search
							className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
							aria-hidden
						/>
						<Input
							ref={searchRef}
							value={query}
							onChange={(event) => setQuery(event.target.value)}
							onKeyDown={(event) => {
								// Down from the box jumps into what was found inside notebooks.
								if (event.key !== "ArrowDown") return;
								const first =
									insideListRef.current?.querySelector<HTMLElement>("[data-search-result]");
								if (first) {
									event.preventDefault();
									first.focus();
								}
							}}
							placeholder={t("search_notebooks")}
							aria-label={t("search_notebooks")}
							className="h-9 rounded-full pl-9"
						/>
					</div>
				</div>
				<div className="hidden items-center gap-2 sm:ml-auto sm:flex">
					<fieldset
						aria-label={t("view_mode")}
						className="hidden h-9 shrink-0 items-center rounded-full border border-primary/10 bg-secondary/50 p-0.5 sm:inline-flex"
					>
						{(
							[
								["grid", LayoutGrid, t("view_grid")],
								["list", List, t("view_list")],
							] as const
						).map(([value, Icon, label]) => (
							<button
								key={value}
								type="button"
								aria-pressed={view === value}
								aria-label={label}
								title={label}
								onClick={() => setView(value)}
								className={cn(
									"flex h-full w-9 items-center justify-center rounded-full text-muted-foreground transition-colors hover:text-foreground",
									view === value && "bg-background text-foreground shadow-sm"
								)}
							>
								<Icon className="size-4" />
							</button>
						))}
					</fieldset>
					{headerLinks}
				</div>
			</header>

			{sections.featured.length > 0 ? (
				<section className="space-y-3">
					<h2 className="text-lg font-semibold">{t("featured")}</h2>
					<div className="grid min-w-0 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
						{sections.featured.map((notebook) => (
							<NotebookCard
								key={notebook.id}
								notebook={notebook}
								href={href(notebook.id)}
								variant="featured"
								{...actionsFor(notebook)}
							/>
						))}
					</div>
				</section>
			) : null}

			<section className="space-y-3">
				<div className="flex items-center justify-between gap-3">
					<h2 className="text-lg font-semibold">{t("recent")}</h2>
					<Button onClick={onCreate} disabled={creating} className="rounded-full">
						{showCreating ? <Spinner size="sm" /> : <Plus className="size-4" />}
						{t("new_notebook")}
					</Button>
				</div>
				{isLoading ? (
					showSkeleton ? (
						<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
							{SKELETON_KEYS.map((key) => (
								<div key={key} className="h-40 animate-pulse rounded-2xl border bg-muted/40" />
							))}
						</div>
					) : null
				) : error && !notebooks ? (
					<div className="flex flex-col items-center gap-3 rounded-2xl border border-dashed py-16 text-center">
						<p className="text-sm text-muted-foreground">{t("load_failed")}</p>
						<Button variant="outline" size="sm" onClick={() => refetch()}>
							<RefreshCw className="size-4" />
							{t("retry")}
						</Button>
					</div>
				) : (
					<div
						className={cn(
							"min-w-0",
							view === "grid"
								? "grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
								: "flex flex-col gap-2"
						)}
					>
						{view === "grid" && !query ? (
							<button
								type="button"
								onClick={onCreate}
								disabled={creating}
								className="flex min-h-40 flex-col items-center justify-center gap-2 rounded-2xl border border-dashed text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
							>
								<span className="flex size-10 items-center justify-center rounded-full bg-muted">
									<Plus className="size-5" />
								</span>
								{t("create")}
							</button>
						) : null}
						{sections.notebooks.map((notebook) => (
							<NotebookCard
								key={notebook.id}
								notebook={notebook}
								href={href(notebook.id)}
								variant={view}
								{...actionsFor(notebook)}
							/>
						))}
					</div>
				)}
				{!isLoading && notebooks?.length === 0 ? (
					<p className="text-sm text-muted-foreground">{t("empty_hint")}</p>
				) : null}
				{!isLoading && (notebooks?.length ?? 0) > 0 && sections.notebooks.length === 0 ? (
					<p className="text-sm text-muted-foreground">{t("no_matches", { query })}</p>
				) : null}
			</section>

			{inside.active ? (
				<ContentSearchResults
					query={inside.query}
					hits={inside.hits}
					settled={inside.settled}
					failed={inside.failed}
					showSpinner={showInsideSpinner}
					notebookHref={href}
					onEscape={() => searchRef.current?.focus()}
					listRef={insideListRef}
				/>
			) : null}
		</div>
	);
}
