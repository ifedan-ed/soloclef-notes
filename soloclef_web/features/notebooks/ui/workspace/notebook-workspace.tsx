"use client";

import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { useCallback, useEffect, useLayoutEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { NOTEBOOKS_HOME } from "@/lib/dashboard-paths";
import { cn } from "@/lib/utils";
import { notebookKeys } from "../../api/notebook-query-keys";
import { type NotebookChanges, notebooksApi } from "../../api/notebooks-api";
import {
	useCopyNotebook,
	useCreateNotebook,
	useSetFeatured,
	useUpdateNotebook,
} from "../../hooks/use-notebook-actions";
import {
	useNotebook,
	useNotebookSources,
	useNotebookThreads,
	useStudioOutputs,
} from "../../hooks/use-notebook-queries";
import { usePanelPreference } from "../../hooks/use-panel-preference";
import {
	type NotebookFocus,
	readNotebookFocus,
	withoutFocusParams,
} from "../../lib/content-search";
import { enabledCount } from "../../lib/source-selection";
import { readStudioLink, withoutStudioLinkParams } from "../../lib/studio-link";
import { NotebookChatProvider } from "../../state/notebook-chat-context";
import { ChatDropZone } from "../chat/chat-drop-zone";
import { NotebookChat } from "../chat/notebook-chat";
import { NotebookRightPanel } from "../notes-tab/notebook-right-panel";
import type { DroppedFiles } from "../sources/add-sources-dialog";
import { SourcesPanel } from "../sources/sources-panel";
import { NotebookHeader } from "./notebook-header";
import { NotebookMenu } from "./notebook-menu";
import { SidePanel } from "./side-panel";

type MobileTab = "sources" | "chat" | "studio";

export function NotebookWorkspace({ notebookId }: { notebookId: number }) {
	const t = useTranslations("notebooks");
	const queryClient = useQueryClient();
	// Its automatic name arrives like any rename: the change stream reports
	// `notebooks` and the notebook and the home list are read again.
	const notebook = useNotebook(notebookId);
	const sources = useNotebookSources(notebookId);
	const [sourcesCollapsed, setSourcesCollapsed] = usePanelPreference("sources");
	const [studioCollapsed, setStudioCollapsed] = usePanelPreference("studio");
	// A link from the home search can name a source (?source=) or note (?note=)
	// to open. Read once, from the router: on a client-side navigation
	// window.location still shows the previous page while this one renders.
	const searchParams = useSearchParams();
	const [focus] = useState<NotebookFocus>(() => readNotebookFocus(searchParams.toString()));
	// A link from the inbox can open Studio (?pane=studio) and run a failed
	// output again (&retry_output=), read once in the same way.
	const [studioLink] = useState(() => readStudioLink(searchParams.toString()));
	// Held here, not in Studio: switching to Notes and back remounts Studio,
	// which must not run the output again. Cleared once Studio has acted.
	const [retryOutputId, setRetryOutputId] = useState(studioLink?.retryOutputId ?? null);
	const retryHandled = useCallback(() => setRetryOutputId(null), []);
	const [mobileTab, setMobileTab] = useState<MobileTab>(
		focus?.kind === "source" ? "sources" : focus?.kind === "note" || studioLink ? "studio" : "chat"
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
	const outputs = useStudioOutputs(notebookId);
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
	const updateNotebook = useUpdateNotebook();
	const setFeatured = useSetFeatured();
	const copyNotebook = useCopyNotebook();
	const { create, creating } = useCreateNotebook();
	// The notebook's one running chat (the newest thread), which the chat panel continues.
	const threads = useNotebookThreads(notebookId);
	const chatThreadId = threads.data?.[0]?.id ?? null;

	// Before the first paint, so a collapsed panel never flashes shut and then open.
	// biome-ignore lint/correctness/useExhaustiveDependencies: acts on the link once
	useLayoutEffect(() => {
		if (studioLink) setStudioCollapsed(false);
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
	// The same for a Studio link: above all, a reload must not run the output again.
	useEffect(() => {
		if (!studioLink) return;
		const timer = window.setTimeout(() => {
			if (readStudioLink(window.location.search) === null) return;
			window.history.replaceState(
				window.history.state,
				"",
				withoutStudioLinkParams(window.location.href)
			);
		}, 0);
		return () => window.clearTimeout(timer);
	}, [studioLink]);

	const sourceList = sources.data ?? [];
	const enabled = enabledCount(sourceList);
	const homeHref = NOTEBOOKS_HOME;

	const update = useCallback(
		(changes: NotebookChanges) => updateNotebook(notebookId, changes),
		[notebookId, updateNotebook]
	);
	const quietUpdate = (changes: NotebookChanges) =>
		update(changes).catch(() => toast.error(t("rename_failed")));

	const openSources = useCallback(() => {
		setSourcesCollapsed(false);
		setMobileTab("sources");
	}, [setSourcesCollapsed]);
	// A source the chat's add_sources card asked to open: its document, and a
	// count, so that asking for the same source again opens it again.
	const [sourceRequest, setSourceRequest] = useState<{ documentId: number; token: number } | null>(
		null
	);
	const openSource = useCallback(
		(documentId: number) => {
			openSources();
			setSourceRequest((previous) => ({ documentId, token: (previous?.token ?? 0) + 1 }));
		},
		[openSources]
	);
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
		await notebooksApi.deleteChatHistory(notebookId);
		await queryClient.invalidateQueries({
			queryKey: notebookKeys.threads(notebookId),
		});
		toast.success(t("delete_history.done"));
	}, [queryClient, notebookId, t]);

	const data = notebook.data;
	const chatContext = useMemo(
		() => ({
			notebookId,
			title: data?.title ?? "",
			icon: data?.icon ?? null,
			createdAt: data?.created_at ?? null,
			sourceCount: sourceList.length,
			enabledSourceCount: enabled,
			threadId: chatThreadId,
			openSources,
			openSource,
			addSources: openAddSources,
			uploadSources,
			openResearch,
			showChat,
		}),
		[
			notebookId,
			data?.title,
			data?.icon,
			data?.created_at,
			sourceList.length,
			enabled,
			chatThreadId,
			openSources,
			openSource,
			openAddSources,
			uploadSources,
			openResearch,
			showChat,
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
							// Only Studio outputs can be shared (each from its own view), so the
							// notebook's menu offers no public link to the notebook or its chat.
							<NotebookMenu
								notebook={data}
								onUpdate={update}
								onCopy={() => copyNotebook(data)}
								onSetFeatured={(featured) => setFeatured(notebookId, featured)}
								onDeleteHistory={deleteHistory}
							/>
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
							{tab === "sources"
								? `${t("sources")} (${enabled})`
								: tab === "studio"
									? t("studio_and_notes")
									: t(tab)}
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
							notebookId={notebookId}
							addOpen={addOpen}
							onAddOpenChange={changeAddOpen}
							dropped={dropped}
							onDropFiles={dropFiles}
							researchFocusToken={researchFocusToken}
							initialOpenSourceId={focus?.kind === "source" ? focus.id : null}
							openDocument={sourceRequest}
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
						<NotebookChat notebookId={notebookId} />
					</ChatDropZone>
					<NotebookRightPanel
						notebookId={notebookId}
						notebookTitle={data?.title ?? ""}
						sources={sourceList}
						onAddSources={openAddSources}
						onResearch={openResearch}
						openOutputId={openOutputId}
						onOpenOutput={changeOpenOutput}
						viewingOutput={!!viewing}
						collapsed={studioCollapsed}
						onCollapsedChange={setStudioCollapsed}
						mobileHidden={mobileTab !== "studio"}
						focusNoteId={focus?.kind === "note" ? focus.id : null}
						retryOutputId={retryOutputId}
						onRetryHandled={retryHandled}
					/>
				</div>
			</div>
		</NotebookChatProvider>
	);
}
