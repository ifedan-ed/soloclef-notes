"use client";

import { Plus } from "lucide-react";
import { useTranslations } from "next-intl";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { useDropzone } from "react-dropzone";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { useGoogleDriveReturn } from "@/features/google-drive-connect/hooks/use-google-drive-return";
import { useNextcloudRefreshable } from "@/features/nextcloud-files/hooks/use-nextcloud-refreshable";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { useNotebookSources, useSourceMutations } from "../../hooks/use-notebook-queries";
import { nextSelectAllValue, selectAllState } from "../../lib/source-selection";
import { AddSourcesDialog, type DroppedFiles } from "./add-sources-dialog";
import { FileDropOverlay } from "./file-drop-overlay";
import { ResearchPanel } from "./research/research-panel";
import { SourceGuideView } from "./source-guide";
import { SourceRow } from "./source-row";
import { SourcesEmptyState } from "./sources-empty-state";

export function SourcesPanel({
	workspaceId,
	notebookId,
	addOpen,
	onAddOpenChange,
	dropped,
	onDropFiles,
	researchFocusToken,
	initialOpenSourceId = null,
}: {
	workspaceId: number;
	notebookId: number;
	addOpen: boolean;
	onAddOpenChange: (open: boolean) => void;
	/** The latest drop, shown pre-filled in the dialog's Upload tab. */
	dropped: DroppedFiles | null;
	/** Files dropped (or browsed) here: opens the dialog's Upload tab with them. */
	onDropFiles: (files: File[]) => void;
	/** Each new non-zero value scrolls to the web research box and focuses its query. */
	researchFocusToken?: number;
	/** A source whose guide opens straight away (a home search result's). */
	initialOpenSourceId?: number | null;
}) {
	const t = useTranslations("notebooks");
	const { data: sources = [], isLoading } = useNotebookSources(workspaceId, notebookId);
	const { toggle, add, remove, refresh } = useSourceMutations(workspaceId, notebookId);
	const nextcloudRefreshable = useNextcloudRefreshable(workspaceId);
	const [openSourceId, setOpenSourceId] = useState<number | null>(initialOpenSourceId);
	const [researchFocus, setResearchFocus] = useState(0);
	// Back from connecting Google Drive: reopen Add sources where the picker is.
	const [driveReturn, setDriveReturn] = useState(0);
	useGoogleDriveReturn(
		useCallback(() => {
			setDriveReturn((n) => n + 1);
			onAddOpenChange(true);
		}, [onAddOpenChange])
	);
	const lastFocusToken = useRef(0);
	const selectAllId = useId();
	const showSkeleton = useDelayedFlag(isLoading);
	const allState = selectAllState(sources);
	// Only documents have a guide; a folder source just shows the list.
	const openSource =
		sources.find((source) => source.id === openSourceId && source.kind === "document") ?? null;

	const { mutateAsync: addSources } = add;
	// A drop never uploads straight away: it opens the Upload tab pre-filled,
	// where the parser and vision choices are made (same as Add sources).
	const { getRootProps, getInputProps, isDragActive, open } = useDropzone({
		onDrop: (files) => {
			if (files.length > 0) onDropFiles(files);
		},
		noClick: true,
		noKeyboard: true,
	});

	useEffect(() => {
		if (!researchFocusToken || researchFocusToken === lastFocusToken.current) return;
		lastFocusToken.current = researchFocusToken;
		setOpenSourceId(null);
		setResearchFocus(researchFocusToken);
	}, [researchFocusToken]);
	const clearResearchFocus = useCallback(() => setResearchFocus(0), []);

	const onError = () => toast.error(t("sources_update_failed"));

	// The dialog stays outside the drop zone: React events bubble through its
	// portal, and its own upload tab already handles drops.
	return (
		<>
			<div {...getRootProps({ className: "relative flex min-h-0 flex-1 flex-col outline-none" })}>
				<input {...getInputProps()} />
				{isDragActive ? <FileDropOverlay /> : null}
				{openSource ? (
					<SourceGuideView
						workspaceId={workspaceId}
						notebookId={notebookId}
						source={openSource}
						onBack={() => setOpenSourceId(null)}
					/>
				) : (
					<div className="flex min-h-0 flex-1 flex-col gap-2">
						<div className="px-3">
							<Button
								variant="outline"
								className="w-full rounded-full"
								onClick={() => onAddOpenChange(true)}
							>
								<Plus className="size-4" />
								{t("add_sources")}
							</Button>
						</div>
						<div className="min-h-0 flex-1 space-y-2 overflow-y-auto px-3 pb-3">
							<ResearchPanel
								workspaceId={workspaceId}
								notebookId={notebookId}
								focusRequest={researchFocus}
								onFocusHandled={clearResearchFocus}
							/>
							{sources.length > 0 ? (
								<div className="flex items-center gap-2 px-2 py-1 text-sm text-muted-foreground">
									<label htmlFor={selectAllId} className="flex-1 cursor-pointer">
										{t("select_all")}
									</label>
									<Checkbox
										id={selectAllId}
										checked={
											allState === "all" ? true : allState === "some" ? "indeterminate" : false
										}
										onCheckedChange={() =>
											toggle.mutate(
												{ sourceId: "all", enabled: nextSelectAllValue(allState) },
												{ onError }
											)
										}
									/>
								</div>
							) : null}
							{isLoading ? (
								showSkeleton ? (
									<div className="space-y-2 px-2" aria-busy>
										{["a", "b", "c"].map((key) => (
											<Skeleton key={key} className="h-7 w-full" />
										))}
									</div>
								) : null
							) : sources.length === 0 ? (
								<SourcesEmptyState onBrowseFiles={open} />
							) : (
								<div>
									{sources.map((source) => (
										<SourceRow
											key={source.id}
											source={source}
											onToggle={(enabled) =>
												toggle.mutate({ sourceId: source.id, enabled }, { onError })
											}
											onOpen={() => setOpenSourceId(source.id)}
											onRemove={() => remove.mutate(source.id, { onError })}
											nextcloudConnectorId={
												source.kind === "document"
													? nextcloudRefreshable?.get(source.target_id)
													: undefined
											}
										/>
									))}
								</div>
							)}
						</div>
					</div>
				)}
			</div>
			<AddSourcesDialog
				open={addOpen}
				onOpenChange={onAddOpenChange}
				workspaceId={workspaceId}
				notebookId={notebookId}
				onAdd={addSources}
				onImported={refresh}
				dropped={dropped}
				connectorTabRequest={driveReturn}
			/>
		</>
	);
}
