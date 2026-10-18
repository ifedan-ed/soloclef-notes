"use client";

import { Folder, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import type { MentionedDocumentInfo } from "@/atoms/chat/mentioned-documents.atom";
import { getDocumentTypeIcon } from "@/components/documents/DocumentTypeIcon";
import { DocumentMentionPicker } from "@/components/new-chat/document-mention-picker";
import {
	type DocumentUploadResult,
	DocumentUploadTab,
} from "@/components/sources/DocumentUploadTab";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent } from "@/components/ui/tabs";
import { useGooglePickerOpen } from "@/features/google-drive-connect/hooks/use-google-picker-open";
import type { SourceTarget } from "../../api/notebooks-api";
import { AddSourcesTabBar } from "./add-sources-tab-bar";
import { ConnectorSourcesTab } from "./connector-sources-tab";

type Pick = Extract<MentionedDocumentInfo, { kind: "doc" | "folder" }>;

function isSourcePick(mention: MentionedDocumentInfo): mention is Pick {
	return mention.kind === "doc" || mention.kind === "folder";
}

interface AddSourcesDialogProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	workspaceId: number;
	notebookId: number;
	onAdd: (targets: SourceTarget[]) => Promise<unknown>;
	/** Files imported from a connector were already linked; refresh the list. */
	onImported: () => void;
	/**
	 * Files dropped on the notebook (Sources panel or chat). The dialog opens
	 * on the Upload tab with them pre-filled, so the Local/Cloud parser and
	 * vision choices are made before anything uploads.
	 */
	dropped?: DroppedFiles | null;
	/** Each new value opens the dialog on the connector tab (back from Google's consent). */
	connectorTabRequest?: number;
}

/** One drop; ``id`` changes per drop so the upload tab starts fresh. */
export interface DroppedFiles {
	id: number;
	files: File[];
}

/**
 * Upload new files (the regular upload tab, with its Local/Cloud parser
 * choice), pick existing documents and folders with the composer's picker,
 * or pick files straight from a connected Nextcloud.
 */
export function AddSourcesDialog({
	open,
	onOpenChange,
	workspaceId,
	notebookId,
	onAdd,
	onImported,
	dropped,
	connectorTabRequest,
}: AddSourcesDialogProps) {
	const t = useTranslations("notebooks");
	const [tab, setTab] = useState(dropped ? "upload" : "library");
	const dropId = dropped?.id;
	useEffect(() => {
		if (dropId !== undefined) setTab("upload");
	}, [dropId]);
	useEffect(() => {
		if (connectorTabRequest) setTab("connector");
	}, [connectorTabRequest]);
	const [search, setSearch] = useState("");
	const [picked, setPicked] = useState<Pick[]>([]);
	const [adding, setAdding] = useState(false);
	const pickerOpen = useGooglePickerOpen();

	const reset = () => {
		setPicked([]);
		setSearch("");
	};

	const handlePicked = useCallback((mentions: MentionedDocumentInfo[]) => {
		setPicked(mentions.filter(isSourcePick));
	}, []);

	const targets = useMemo<SourceTarget[]>(
		() =>
			picked.map((item) => ({
				kind: item.kind === "folder" ? "folder" : "document",
				id: item.id,
			})),
		[picked]
	);

	const add = async (items: SourceTarget[]) => {
		if (items.length === 0) return;
		setAdding(true);
		try {
			await onAdd(items);
			reset();
			onOpenChange(false);
		} catch {
			toast.error(t("add_sources_failed"));
		} finally {
			setAdding(false);
		}
	};

	const onUploaded = (result: DocumentUploadResult) => {
		const items: SourceTarget[] = [
			...result.documentIds.map((id) => ({ kind: "document" as const, id })),
			...(result.folderId ? [{ kind: "folder" as const, id: result.folderId }] : []),
		];
		void add(items);
	};

	return (
		<Dialog
			open={open}
			// The Google Picker opens outside this dialog; while it is up the
			// dialog must neither block it nor close when it is clicked.
			modal={!pickerOpen}
			onOpenChange={(next) => {
				if (!next && pickerOpen) return;
				if (!next) reset();
				onOpenChange(next);
			}}
		>
			<DialogContent className="flex max-h-[85vh] w-[calc(100vw-2rem)] max-w-2xl flex-col gap-4 select-text">
				<DialogHeader>
					<DialogTitle>{t("add_sources")}</DialogTitle>
					<DialogDescription>{t("add_sources_description")}</DialogDescription>
				</DialogHeader>
				<Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
					<AddSourcesTabBar />
					<TabsContent value="library" className="flex min-h-0 flex-1 flex-col gap-3">
						<Input
							value={search}
							onChange={(event) => setSearch(event.target.value)}
							placeholder={t("search_library")}
							aria-label={t("search_library")}
						/>
						<div className="min-h-64 flex-1 overflow-y-auto rounded-lg border">
							<DocumentMentionPicker
								workspaceId={workspaceId}
								documentsOnly
								onSelectionChange={handlePicked}
								onDone={() => {}}
								initialSelectedDocuments={picked}
								externalSearch={search}
							/>
						</div>
						{picked.length > 0 ? (
							<div className="flex max-h-24 flex-wrap gap-1.5 overflow-y-auto">
								{picked.map((item) => (
									<span
										key={`${item.kind}-${item.id}`}
										className="inline-flex max-w-60 items-center gap-1 rounded-full bg-secondary px-2 py-1 text-xs"
									>
										{item.kind === "folder" ? (
											<Folder className="size-3.5" />
										) : (
											getDocumentTypeIcon(item.document_type, "size-3.5")
										)}
										<span className="truncate">{item.title}</span>
										<button
											type="button"
											onClick={() =>
												setPicked((current) =>
													current.filter((c) => !(c.kind === item.kind && c.id === item.id))
												)
											}
											aria-label={t("remove_pick", { title: item.title })}
											className="rounded-full p-0.5 hover:bg-background"
										>
											<X className="size-3" />
										</button>
									</span>
								))}
							</div>
						) : null}
						<div className="flex justify-end gap-2">
							<Button variant="ghost" onClick={() => onOpenChange(false)}>
								{t("cancel")}
							</Button>
							<Button disabled={targets.length === 0 || adding} onClick={() => add(targets)}>
								{t("add_n_sources", { count: targets.length })}
							</Button>
						</div>
					</TabsContent>
					<TabsContent value="upload" className="min-h-0 flex-1 overflow-y-auto">
						<DocumentUploadTab
							key={dropped?.id ?? "picked"}
							workspaceId={String(workspaceId)}
							onSuccess={onUploaded}
							initialFiles={dropped?.files}
						/>
					</TabsContent>
					<TabsContent value="connector" className="flex min-h-0 flex-1 flex-col overflow-y-auto">
						<ConnectorSourcesTab
							workspaceId={workspaceId}
							notebookId={notebookId}
							onLeave={() => onOpenChange(false)}
							onPickFromLibrary={() => setTab("library")}
							onImported={() => {
								onImported();
								onOpenChange(false);
							}}
						/>
					</TabsContent>
				</Tabs>
			</DialogContent>
		</Dialog>
	);
}
