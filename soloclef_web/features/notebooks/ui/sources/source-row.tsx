"use client";

import { Folder, MoreVertical, RefreshCw, Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { getDocumentTypeIcon } from "@/components/documents/DocumentTypeIcon";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Spinner } from "@/components/ui/spinner";
import type { DocumentStage } from "@/contracts/types/document.types";
import { ReindexDialog } from "@/features/documents/reindex/ui/reindex-dialog";
import { RefreshFromNextcloudItem } from "@/features/nextcloud-files/ui/refresh-from-nextcloud-item";
import { SOURCE_STAGE_KEYS } from "../../lib/source-stages";
import type { NotebookSource } from "../../model/notebook";

interface SourceRowProps {
	source: NotebookSource;
	onToggle: (enabled: boolean) => void;
	onOpen: () => void;
	onRemove: () => void;
	/** Called when the document was queued for indexing again, to re-read the list. */
	onReindexed: () => void;
	/** Set when this document was picked from Nextcloud by this user. */
	nextcloudConnectorId?: number;
	/** The indexing step a processing document is on, when the server has said. */
	stage?: DocumentStage;
}

export function SourceRow({
	source,
	onToggle,
	onOpen,
	onRemove,
	onReindexed,
	nextcloudConnectorId,
	stage,
}: SourceRowProps) {
	const t = useTranslations("notebooks");
	const indexing = source.status === "pending" || source.status === "processing";
	const failed = source.status === "failed";
	// "never" until the dialog is first asked for: a list of sources then
	// mounts no dialog, and no parser query, per row. After that it stays
	// mounted so that closing it can animate.
	const [reindex, setReindex] = useState<"never" | "open" | "closed">("never");
	return (
		<div className="group flex min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-accent">
			<span className="flex size-5 shrink-0 items-center justify-center text-muted-foreground">
				{source.kind === "folder" ? (
					<Folder className="size-4" />
				) : (
					getDocumentTypeIcon(source.document_type ?? "FILE", "size-4")
				)}
			</span>
			<button
				type="button"
				onClick={onOpen}
				disabled={source.kind === "folder"}
				className="min-w-0 flex-1 text-left text-sm text-foreground disabled:cursor-default"
				title={source.title}
			>
				<span className="block truncate">{source.title}</span>
				{/* Under the title, not beside it: the panel is narrow, and a
				    label on the same line would push a long title out of view. */}
				{indexing && stage ? (
					<span
						className="block truncate text-xs text-muted-foreground"
						data-testid="source-stage"
						aria-live="polite"
					>
						{t(SOURCE_STAGE_KEYS[stage])}
					</span>
				) : failed ? (
					// Said in words, with the way out one tap away in the menu:
					// a source that silently is not searched looks like one that is.
					<span className="block truncate text-xs text-destructive" data-testid="source-failed">
						{t("source_index_failed")}
					</span>
				) : null}
			</button>
			{indexing ? (
				<span className="shrink-0" title={t("source_indexing")}>
					<Spinner size="xs" />
				</span>
			) : null}
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						variant="ghost"
						size="icon"
						className="size-7 shrink-0 rounded-full text-muted-foreground opacity-100 md:opacity-0 md:group-hover:opacity-100 data-[state=open]:opacity-100"
						aria-label={t("more_actions")}
					>
						<MoreVertical className="size-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					{nextcloudConnectorId != null ? (
						<RefreshFromNextcloudItem
							connectorId={nextcloudConnectorId}
							documentId={source.target_id}
						/>
					) : null}
					{source.kind === "document" ? (
						// Off while a job is already on its way: the server would
						// answer that the document is being indexed.
						<DropdownMenuItem disabled={indexing} onSelect={() => setReindex("open")}>
							<RefreshCw className="size-4" />
							{t("reindex_source")}
						</DropdownMenuItem>
					) : null}
					<DropdownMenuItem className="text-destructive focus:text-destructive" onSelect={onRemove}>
						<Trash2 className="size-4" />
						{t("remove_source")}
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			{source.kind === "document" && reindex !== "never" ? (
				<ReindexDialog
					open={reindex === "open"}
					onOpenChange={(open) => setReindex(open ? "open" : "closed")}
					documentId={source.target_id}
					title={source.title}
					onQueued={onReindexed}
				/>
			) : null}
			<Checkbox
				checked={source.enabled}
				onCheckedChange={(value) => onToggle(value === true)}
				aria-label={t("use_source", { title: source.title })}
				className="shrink-0"
			/>
		</div>
	);
}
