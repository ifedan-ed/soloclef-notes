"use client";

import { Folder, MoreVertical, Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";
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
import { RefreshFromNextcloudItem } from "@/features/nextcloud-files/ui/refresh-from-nextcloud-item";
import type { NotebookSource } from "../../model/notebook";

interface SourceRowProps {
	source: NotebookSource;
	onToggle: (enabled: boolean) => void;
	onOpen: () => void;
	onRemove: () => void;
	/** Set when this document was picked from Nextcloud by this user. */
	nextcloudConnectorId?: number;
}

export function SourceRow({
	source,
	onToggle,
	onOpen,
	onRemove,
	nextcloudConnectorId,
}: SourceRowProps) {
	const t = useTranslations("notebooks");
	const indexing = source.status === "pending" || source.status === "processing";
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
				className="min-w-0 flex-1 truncate text-left text-sm text-foreground disabled:cursor-default"
				title={source.title}
			>
				{source.title}
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
					<DropdownMenuItem className="text-destructive focus:text-destructive" onSelect={onRemove}>
						<Trash2 className="size-4" />
						{t("remove_source")}
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			<Checkbox
				checked={source.enabled}
				onCheckedChange={(value) => onToggle(value === true)}
				aria-label={t("use_source", { title: source.title })}
				className="shrink-0"
			/>
		</div>
	);
}
