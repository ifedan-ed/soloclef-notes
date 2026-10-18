"use client";

import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { useCallback, useEffect, useLayoutEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
	NotebookShareDialog,
	publicLinksApi,
	ShareDialog,
	SharedBadge,
	useCanCreatePublicLinks,
	useLiveNotebookLink,
} from "@/features/public-sharing";
import { cn } from "@/lib/utils";
import { workspaceHomePath } from "@/lib/workspace-home";
import { notebookKeys } from "../../api/notebook-query-keys";
import { type NotebookChanges, notebooksApi } from "../../api/notebooks-api";
import { useNotebookWithAutoTitle } from "../../hooks/use-auto-title";
import {
	useCopyNotebook,
	useCreateNotebook,
	useSetFeatured,
	useUpdateNotebook,
} from "../../hooks/use-notebook-actions";
import { useNotebookThreads, useStudioOutputs } from "../../hooks/use-notebook-queries";
import { usePanelPreference } from "../../hooks/use-panel-preference";
import {
	type NotebookFocus,
	readNotebookFocus,
	withoutFocusParams,
} from "../../lib/content-search";
import { noteFromAnswer } from "../../lib/note-from-answer";
import { enabledCount } from "../../lib/source-selection";
import type { NotebookNote } from "../../model/notebook";
import { NotebookChatProvider } from "../../state/notebook-chat-context";
import { ChatDropZone } from "../chat/chat-drop-zone";
import { NotebookChat } from "../chat/notebook-chat";
import type { DroppedFiles } from "../sources/add-sources-dialog";
import { SourcesPanel } from "../sources/sources-panel";
import { StudioPanel } from "../studio/studio-panel";
import { NotebookHeader } from "./notebook-header";
import { NotebookMenu } from "./notebook-menu";
import { SidePanel } from "./side-panel";

type MobileTab = "sources" | "chat" | "studio";

export function NotebookWorkspace({
	workspaceId,
	notebookId,
}: {
	workspaceId: number;
	notebookId: number;
}) {
	const t = useTranslations("notebooks");
	const queryClient = useQueryClient();
	const { notebook, sources, watchTitle } = useNotebookWithAutoTitle(workspaceId, notebookId);
	const [sourcesCollapsed, setSourcesCollapsed] = usePanelPreference("sources");
	const [studioCollapsed, setStudioCollapsed] = usePanelPreference("studio");
	// A link from the home search can name a source (?source=) or note (?note=)
	// to open. Read once, from the router: on a client-side navigation
	// window.location still shows the previous page while this one renders.
	const searchParams = useSearchParams();
	const [focus] = useState<NotebookFocus>(() => readNotebookFocus(searchParams.toString()));
	const [mobileTab, setMobileTab] = useState<MobileTab>(
		focus?.kind === "source" ? "sources" : focus?.kind === "note" ? "studio" : "chat"
	);
	const [addOpen, setAddOpen] = useState(false);
	// The latest drop on the Sources panel or chat, pre-filled in the dialog.
	const [dropped, setDropped] = useState<DroppedFiles | null>(null);
	const [researchFocusToken, setResearchFocusToken] = useState(0);
	// The Studio output open in the output view. On a desktop the Studio
	// column widens for it and the Sources column steps aside (until the user
	// opens it again); on a phone it fills the screen over the Studio tab.
	const [openOutputId, setOpenOutputId] = useState<number | null>(null);
	const [sourcesYield, setSourcesYield] = useState(true);
	const outputs = useStudioOutputs(workspaceId, notebookId);
	const openOutput =
		openOutputId === null
			? null
			: ((outputs.data ?? []).find((output) => output.id === openOutputId) ?? null);
	const viewing = openOutput?.artifact_id ? openOutput : null;
	const changeOpenOutput = useCallback((outputId: number | null) => {
		setOpenOutputId(outputId);
		if (outputId !== null) setSourcesYield(true);
	}, []);
	// A deleted output closes its view.
	useEffect(() => {
		if (openOutputId !== null && outputs.data && !openOutput) setOpenOutputId(null);
	}, [openOutputId, outputs.data, openOutput]);
	const updateNotebook = useUpdateNotebook(workspaceId);
	const setFeatured = useSetFeatured(workspaceId);
	const copyNotebook = useCopyNotebook(workspaceId);
	const { create, creating } = useCreateNotebook(workspaceId);
	// The notebook's one running chat (the newest thread) is what "Share chat" shares.
	const threads = useNotebookThreads(workspaceId, notebookId);
	const chatThreadId = threads.data?.[0]?.id ?? null;
	const canShare = useCanCreatePublicLinks();
	const [shareOpen, setShareOpen] = useState(false);
	// The notebook's live link: "Share notebook…" and the "Shared" badge.
	const liveLink = useLiveNotebookLink(workspaceId, notebookId);
	const [notebookShareOpen, setNotebookShareOpen] = useState(false);

	// Before the first paint, so a collapsed panel never flashes shut and then open.
	// biome-ignore lint/correctness/useExhaustiveDependencies: acts on the link once
	useLayoutEffect(() => {
		if (!focus) return;
		if (focus.kind === "source") setSourcesCollapsed(false);
		else setStudioCollapsed(false);
	}, []);
	// Then drop the parameter, so a reload later does not reopen it. Deferred:
	// the router writes the new URL after this page's own effects have run.
	useEffect(() => {
		if (!focus) return;
		const timer = window.setTimeout(() => {
			if (readNotebookFocus(window.location.search) === null) return;
			window.history.replaceState(
				window.history.state,
				"",
				withoutFocusParams(window.location.href)
			);
		}, 0);
		return () => window.clearTimeout(timer);
	}, [focus]);

	const sourceList = sources.data ?? [];
	const enabled = enabledCount(sourceList);
	const homeHref = workspaceHomePath(workspaceId);

	const update = useCallback(
		(changes: NotebookChanges) => updateNotebook(notebookId, changes),
		[notebookId, updateNotebook]
	);
	const quietUpdate = (changes: NotebookChanges) =>
		update(changes).catch(() => toast.error(t("rename_failed")));

	const saveToNote = useCallback(
		async (markdown: string) => {
			const { title, content } = noteFromAnswer(markdown);
			const note = await notebooksApi.createNote(workspaceId, notebookId, {
				title: title || t("new_note"),
				content,
				origin: "chat",
			});
			queryClient.setQueryData<NotebookNote[]>(
				notebookKeys.notes(workspaceId, notebookId),
				(current = []) => [note, ...current]
			);
			toast.success(t("saved_to_note"));
		},
		[queryClient, workspaceId, notebookId, t]
	);

	const openSources = useCallback(() => {
		setSourcesCollapsed(false);
		setMobileTab("sources");
	}, [setSourcesCollapsed]);
	const openAddSources = useCallback(() => {
		openSources();
		setDropped(null);
		setAddOpen(true);
	}, [openSources]);
	// Opening the dialog by hand starts clean; a drop opens it pre-filled.
	const changeAddOpen = useCallback((open: boolean) => {
		if (open) setDropped(null);
		setAddOpen(open);
	}, []);
	const dropFiles = useCallback(
		(files: File[]) => {
			openSources();
			setDropped({ id: Date.now(), files });
			setAddOpen(true);
		},
		[openSources]
	);
	const uploadSources = useCallback(() => dropFiles([]), [dropFiles]);
	const openResearch = useCallback(() => {
		openSources();
		setResearchFocusToken((token) => token + 1);
	}, [openSources]);
	const showChat = useCallback(() => setMobileTab("chat"), []);

	const deleteHistory = useCallback(async () => {
		await notebooksApi.deleteChatHistory(workspaceId, notebookId);
		await queryClient.invalidateQueries({
			queryKey: notebookKeys.threads(workspaceId, notebookId),
		});
		toast.success(t("delete_history.done"));
	}, [queryClient, workspaceId, notebookId, t]);

	const data = notebook.data;
	const chatContext = useMemo(
		() => ({
			workspaceId,
			notebookId,
			title: data?.title ?? "",
			icon: data?.icon ?? null,
			createdAt: data?.created_at ?? null,
			sourceCount: sourceList.length,
			enabledSourceCount: enabled,
			saveToNote,
			openSources,
			addSources: openAddSources,
			uploadSources,
			openResearch,
			showChat,
			watchTitle,
		}),
		[
			workspaceId,
			notebookId,
			data?.title,
			data?.icon,
			data?.created_at,
			sourceList.length,
			enabled,
			saveToNote,
			openSources,
			openAddSources,
			uploadSources,
			openResearch,
			showChat,
			watchTitle,
		]
	);

	if (notebook.error && !notebook.data) {
		return (
			<div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center">
				<p className="text-sm text-muted-foreground">{t("notebook_not_found")}</p>
				<Button variant="outline" onClick={() => notebook.refetch()}>
					{t("retry")}
				</Button>
			</div>
		);
	}

	const tabs: MobileTab[] = ["sources", "chat", "studio"];

	return (
		<NotebookChatProvider value={chatContext}>
			{chatThreadId !== null ? (
				<ShareDialog
					open={shareOpen}
					onOpenChange={setShareOpen}
					workspaceId={workspaceId}
					subject="chat"
					itemTitle={data?.title ?? ""}
					create={(expiry) => publicLinksApi.shareChat(chatThreadId, expiry)}
				/>
			) : null}
			{canShare ? (
				<NotebookShareDialog
					open={notebookShareOpen}
					onOpenChange={setNotebookShareOpen}
					workspaceId={workspaceId}
					notebookId={notebookId}
					notebookTitle={data?.title ?? ""}
					link={liveLink.link}
					onChange={liveLink.set}
				/>
			) : null}
			<div className="flex h-full min-h-0 flex-col">
				<NotebookHeader
					notebook={data}
					homeHref={homeHref}
					onRename={(title) => void quietUpdate({ title })}
					onIconChange={(icon) => void quietUpdate({ icon })}
					onCreate={() => void create().catch(() => toast.error(t("create_failed")))}
					creating={creating}
					menu={
						data ? (
							<>
								<SharedBadge
									workspaceId={workspaceId}
									notebookId={notebookId}
									link={liveLink.link}
									onSettings={canShare ? () => setNotebookShareOpen(true) : undefined}
									onStopped={() => liveLink.set(null)}
								/>
								<NotebookMenu
									notebook={data}
									onUpdate={update}
									onCopy={() => copyNotebook(data)}
									onSetFeatured={(featured) => setFeatured(notebookId, featured)}
									onDeleteHistory={deleteHistory}
									onSharePublicly={
										canShare && chatThreadId !== null ? () => setShareOpen(true) : undefined
									}
									onShareNotebook={canShare ? () => setNotebookShareOpen(true) : undefined}
								/>
							</>
						) : null
					}
				/>
				<div role="tablist" className="flex shrink-0 gap-1 border-b px-2 py-1.5 lg:hidden">
					{tabs.map((tab) => (
						<button
							key={tab}
							type="button"
							role="tab"
							aria-selected={mobileTab === tab}
							onClick={() => setMobileTab(tab)}
							className={cn(
								"flex-1 rounded-full px-3 py-1.5 text-sm font-medium transition-colors",
								mobileTab === tab
									? "bg-primary text-primary-foreground"
									: "text-muted-foreground hover:bg-accent"
							)}
						>
							{tab === "sources" ? `${t("sources")} (${enabled})` : t(tab)}
						</button>
					))}
				</div>
				<div className="flex min-h-0 flex-1">
					<SidePanel
						side="left"
						title={t("sources")}
						collapsed={viewing && sourcesYield ? true : sourcesCollapsed}
						onCollapsedChange={(collapsed) => {
							if (viewing) setSourcesYield(collapsed);
							if (!viewing || !collapsed) setSourcesCollapsed(collapsed);
						}}
						collapseLabel={t("collapse_sources")}
						expandLabel={t("expand_sources")}
						mobileHidden={mobileTab !== "sources"}
					>
						<SourcesPanel
							workspaceId={workspaceId}
							notebookId={notebookId}
							addOpen={addOpen}
							onAddOpenChange={changeAddOpen}
							dropped={dropped}
							onDropFiles={dropFiles}
							researchFocusToken={researchFocusToken}
							initialOpenSourceId={focus?.kind === "source" ? focus.id : null}
						/>
					</SidePanel>
					<ChatDropZone
						onDropFiles={dropFiles}
						className={cn(
							"relative min-h-0 min-w-0 flex-1 flex-col",
							mobileTab === "chat" ? "flex" : "hidden lg:flex"
						)}
					>
						{viewing && mobileTab !== "studio" ? (
							// A phone: the open output waits behind the Studio tab, where it was.
							<div className="pointer-events-none absolute inset-x-0 top-2 z-20 flex justify-center px-3 lg:hidden">
								<Button
									size="sm"
									variant="secondary"
									className="pointer-events-auto h-8 max-w-full rounded-full shadow-md"
									onClick={() => setMobileTab("studio")}
								>
									<ArrowLeft className="size-4" />
									<span className="truncate">{t("back_to_output", { title: viewing.title })}</span>
								</Button>
							</div>
						) : null}
						<NotebookChat workspaceId={workspaceId} notebookId={notebookId} />
					</ChatDropZone>
					<SidePanel
						side="right"
						title={t("studio")}
						collapsed={studioCollapsed && !viewing}
						onCollapsedChange={setStudioCollapsed}
						collapseLabel={t("collapse_studio")}
						expandLabel={t("expand_studio")}
						mobileHidden={mobileTab !== "studio"}
						wide={!!viewing}
						bare={!!viewing}
					>
						<StudioPanel
							workspaceId={workspaceId}
							notebookId={notebookId}
							notebookTitle={data?.title ?? ""}
							sources={sourceList}
							onAddSources={openAddSources}
							onResearch={openResearch}
							initialOpenNoteId={focus?.kind === "note" ? focus.id : null}
							openOutputId={openOutputId}
							onOpenOutput={changeOpenOutput}
						/>
					</SidePanel>
				</div>
			</div>
		</NotebookChatProvider>
	);
}
