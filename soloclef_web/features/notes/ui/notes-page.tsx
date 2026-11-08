"use client";

import { useQueryClient } from "@tanstack/react-query";
import { NotebookPen, RefreshCw, Search } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { notebookNoteQuery } from "../api/note-queries";
import { useNoteOpening } from "../hooks/use-note-opening";
import { useWorkspaceNotes } from "../hooks/use-workspace-notes";
import { groupNotes, hasAutomationNotes, type NotesView } from "../lib/note-groups";
import { notesQuery } from "../lib/note-places";
import type { WorkspaceNote } from "../model/workspace-note";
import { DocumentNoteDetail } from "./document-note-detail";
import { NoteGroupSection } from "./note-group-section";
import { NotebookNoteDetail } from "./notebook-note-detail";

const SKELETON_KEYS = ["a", "b", "c", "d"];

/**
 * Every note in the workspace, grouped where it lives: under its notebook,
 * or under its folder of the documents tree. "Automation notes" groups the
 * notes automations made by automation instead.
 */
export function NotesPage({ workspaceId }: { workspaceId: number }) {
	const t = useTranslations("notes");
	const [raw, setRaw] = useState("");
	const queryClient = useQueryClient();
	const [open, setOpen] = useState<WorkspaceNote | null>(null);
	const opening = useNoteOpening(workspaceId, setOpen);
	const [view, setView] = useState<NotesView>("folders");
	const [folded, setFolded] = useState<ReadonlySet<string>>(() => new Set());
	const notes = useWorkspaceNotes(workspaceId, raw);
	const showSkeleton = useDelayedFlag(notes.isLoading);
	const query = notesQuery(raw);

	if (open) {
		return (
			<div className="mx-auto flex min-h-[60vh] w-full min-w-0 max-w-3xl flex-col overflow-hidden rounded-2xl border bg-background select-text">
				{open.kind === "document" ? (
					<DocumentNoteDetail
						key={`document-${open.id}`}
						workspaceId={workspaceId}
						note={open}
						onBack={() => setOpen(null)}
						onMoved={(moved, notebook) => {
							// The moved note is known already: it draws at once, without a fetch.
							queryClient.setQueryData(
								notebookNoteQuery(workspaceId, notebook.id, moved.id).queryKey,
								moved
							);
							setOpen({
								...open,
								kind: "notebook_note",
								id: moved.id,
								title: moved.title,
								notebook: { id: notebook.id, title: notebook.title, icon: notebook.icon },
								folder: null,
								source_in: [],
							});
						}}
					/>
				) : (
					<NotebookNoteDetail
						key={`note-${open.id}`}
						workspaceId={workspaceId}
						note={open}
						onBack={() => setOpen(null)}
					/>
				)}
			</div>
		);
	}

	const list = notes.data ?? [];
	const groups = groupNotes(list, view);
	// Offered once an automation has made a note; kept while it is the view,
	// so a search with no automation note can still switch back.
	const offerViews = view === "automations" || hasAutomationNotes(list);
	const toggle = (key: string) =>
		setFolded((current) => {
			const next = new Set(current);
			if (!next.delete(key)) next.add(key);
			return next;
		});

	return (
		<div className="mx-auto w-full min-w-0 max-w-3xl space-y-6 overflow-x-hidden">
			<header className="flex flex-col gap-3 sm:flex-row sm:items-center">
				<div className="flex items-center gap-2">
					<span className="flex size-7 items-center justify-center rounded-md bg-muted text-muted-foreground">
						<NotebookPen className="size-4" aria-hidden />
					</span>
					<h1 className="text-xl font-semibold leading-tight text-foreground">{t("title")}</h1>
				</div>
				{offerViews ? (
					<SegmentedControl
						value={view}
						onValueChange={(value) => setView(value as NotesView)}
						ariaLabel={t("view_label")}
						options={[
							{ value: "folders", label: t("view_folders") },
							{ value: "automations", label: t("view_automations") },
						]}
					/>
				) : null}
				<div className="relative min-w-0 flex-1 sm:ml-auto sm:max-w-xs">
					<Search
						className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
						aria-hidden
					/>
					<Input
						value={raw}
						onChange={(event) => setRaw(event.target.value)}
						placeholder={t("search")}
						aria-label={t("search")}
						className="h-9 rounded-full pl-9"
					/>
				</div>
			</header>

			{notes.isError && !notes.data ? (
				<div className="flex items-center gap-3 rounded-xl border p-4 text-sm">
					<span className="flex-1 text-muted-foreground">{t("load_failed")}</span>
					<Button size="sm" variant="outline" onClick={() => void notes.refetch()}>
						<RefreshCw className="size-4" />
						{t("retry")}
					</Button>
				</div>
			) : notes.isLoading ? (
				showSkeleton ? (
					<div className="space-y-2">
						{SKELETON_KEYS.map((key) => (
							<Skeleton key={key} className="h-16 w-full rounded-xl" />
						))}
					</div>
				) : null
			) : groups.length === 0 ? (
				<p className="py-10 text-center text-sm text-muted-foreground">
					{query
						? t("empty_search", { query })
						: view === "automations"
							? t("empty_automations")
							: t("empty")}
				</p>
			) : (
				<div className="space-y-5">
					{groups.map((group) => (
						<NoteGroupSection
							key={group.key}
							group={group}
							view={view}
							open={!folded.has(group.key)}
							onToggle={() => toggle(group.key)}
							opening={opening.opening}
							onOpenNote={opening.open}
						/>
					))}
				</div>
			)}
		</div>
	);
}
