"use client";

import { useAtom } from "jotai";
import { useTranslations } from "next-intl";
import { useEffect, useState } from "react";
import { useNotesList } from "@/features/notes/hooks/use-notes-list";
import { cn } from "@/lib/utils";
import type { NotebookSource } from "../../model/notebook";
import { noteToOpenAtom } from "../../state/open-note.atom";
import { StudioPanel } from "../studio/studio-panel";
import { SidePanel } from "../workspace/side-panel";
import { NotebookNotesTab } from "./notebook-notes-tab";

type RightTab = "studio" | "notes";

/**
 * A notebook's right-hand column: two tabs, Studio (what was generated)
 * and Notes (what was written). Notes used to be rows among Studio's
 * outputs; they are the person's own words and have a tab of their own.
 * The column widens for an open output and for an open note, so neither is
 * read or written in a strip.
 */
export function NotebookRightPanel({
	notebookId,
	notebookTitle,
	sources,
	onAddSources,
	onResearch,
	openOutputId,
	onOpenOutput,
	viewingOutput,
	collapsed,
	onCollapsedChange,
	mobileHidden,
	focusNoteId,
}: {
	notebookId: number;
	notebookTitle: string;
	sources: NotebookSource[];
	onAddSources: () => void;
	onResearch: () => void;
	openOutputId: number | null;
	onOpenOutput: (outputId: number | null) => void;
	/** An output is open in the output view: the column is wide and brings its own header. */
	viewingOutput: boolean;
	collapsed: boolean;
	onCollapsedChange: (collapsed: boolean) => void;
	mobileHidden: boolean;
	/** A note to open straight away (a link from a search). */
	focusNoteId: number | null;
}) {
	const t = useTranslations("notebooks");
	const tn = useTranslations("notes");
	const [tab, setTab] = useState<RightTab>(focusNoteId !== null ? "notes" : "studio");
	const [openNoteId, setOpenNoteId] = useState<number | null>(focusNoteId);
	const notes = useNotesList({ notebook: notebookId });
	const noteCount = notes.data?.pages[0]?.total;
	const noteOpen = tab === "notes" && openNoteId !== null;
	// A note the chat asked to show (Save to note's "Open").
	const [asked, setAsked] = useAtom(noteToOpenAtom);
	useEffect(() => {
		if (!asked || asked.notebookId !== notebookId) return;
		setAsked(null);
		if (openOutputId !== null) onOpenOutput(null);
		setTab("notes");
		setOpenNoteId(asked.noteId);
		onCollapsedChange(false);
	}, [asked, notebookId, openOutputId, onOpenOutput, onCollapsedChange, setAsked]);

	const tabs = (
		<div role="tablist" aria-label={t("studio")} className="flex items-center gap-1">
			{(["studio", "notes"] as const).map((value) => (
				<button
					key={value}
					type="button"
					role="tab"
					aria-selected={tab === value}
					onClick={() => {
						// An open output belongs to Studio; leaving the tab closes it.
						if (value === "notes" && openOutputId !== null) onOpenOutput(null);
						setTab(value);
					}}
					className={cn(
						"inline-flex h-8 items-center gap-1.5 rounded-full px-3 text-sm font-medium transition-colors",
						tab === value
							? "bg-accent text-foreground"
							: "text-muted-foreground hover:bg-accent/60 hover:text-foreground"
					)}
				>
					{value === "studio" ? t("studio") : tn("title")}
					{value === "notes" && noteCount ? (
						<span className="rounded-full bg-background px-1.5 text-[11px] tabular-nums text-muted-foreground">
							{noteCount}
						</span>
					) : null}
				</button>
			))}
		</div>
	);

	return (
		<SidePanel
			side="right"
			title={t("studio")}
			heading={tabs}
			collapsed={collapsed && !viewingOutput && !noteOpen}
			onCollapsedChange={onCollapsedChange}
			collapseLabel={t("collapse_studio")}
			expandLabel={t("expand_studio")}
			mobileHidden={mobileHidden}
			wide={viewingOutput || noteOpen}
			bare={viewingOutput && tab === "studio"}
		>
			{tab === "studio" ? (
				<StudioPanel
					notebookId={notebookId}
					notebookTitle={notebookTitle}
					sources={sources}
					onAddSources={onAddSources}
					onResearch={onResearch}
					openOutputId={openOutputId}
					onOpenOutput={onOpenOutput}
				/>
			) : (
				<NotebookNotesTab
					notebookId={notebookId}
					openNoteId={openNoteId}
					onOpenNote={setOpenNoteId}
				/>
			)}
		</SidePanel>
	);
}
