"use client";

import { ChevronRight } from "lucide-react";
import { useFormatter, useTranslations } from "next-intl";
import { Checkbox } from "@/components/ui/checkbox";
import type { RemoteEntry } from "../api/nextcloud-files-api";
import { formatBytes, isPickable, parentPath, type Selection } from "../lib/picker-selection";
import { RemoteEntryIcon } from "./remote-entry-icon";

interface NextcloudFileListProps {
	entries: RemoteEntry[];
	selection: Selection;
	/** Extensions the chosen parser reads; null while capabilities load. */
	accepted: Set<string> | null;
	/** Parser name for the "not supported" hint. */
	parserName: string;
	/** Search results show where each entry lives. */
	showLocation?: boolean;
	onToggle: (entry: RemoteEntry) => void;
	onOpenFolder: (path: string) => void;
}

/**
 * One Nextcloud folder (or search result set) as a checklist: folders open on
 * a tap of their name and can be ticked whole; files the chosen parser cannot
 * read stay visible but cannot be ticked.
 */
export function NextcloudFileList({
	entries,
	selection,
	accepted,
	parserName,
	showLocation = false,
	onToggle,
	onOpenFolder,
}: NextcloudFileListProps) {
	const t = useTranslations("nextcloud_files");
	const format = useFormatter();

	return (
		<ul className="divide-y" aria-label={t("list_label")}>
			{entries.map((entry) => {
				const pickable = isPickable(entry, accepted);
				const checked = selection.has(entry.path);
				const modified = entry.modified
					? format.dateTime(new Date(entry.modified), { dateStyle: "medium" })
					: null;
				const meta = [entry.is_folder ? null : formatBytes(entry.size), modified]
					.filter(Boolean)
					.join(" · ");
				const location = showLocation ? parentPath(entry.path) : null;
				const checkboxId = `nc-${entry.path}`;
				return (
					<li
						key={entry.path}
						data-unsupported={pickable ? undefined : "true"}
						className={`flex min-h-12 items-center gap-3 px-3 py-2 ${pickable ? "" : "opacity-55"}`}
					>
						<Checkbox
							id={checkboxId}
							checked={checked}
							disabled={!pickable}
							onCheckedChange={() => onToggle(entry)}
							aria-label={t(entry.is_folder ? "pick_folder" : "pick_file", { name: entry.name })}
							className="size-5"
						/>
						<RemoteEntryIcon entry={entry} />
						{entry.is_folder ? (
							<button
								type="button"
								onClick={() => onOpenFolder(entry.path)}
								className="flex min-w-0 flex-1 items-center gap-2 rounded text-left hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
								aria-label={t("open_folder", { name: entry.name })}
							>
								<span className="min-w-0 flex-1">
									<span className="block truncate text-sm font-medium">{entry.name}</span>
									{location || meta ? (
										<span className="block truncate text-xs text-muted-foreground">
											{location ?? meta}
										</span>
									) : null}
								</span>
								<ChevronRight
									aria-hidden="true"
									className="size-4 shrink-0 text-muted-foreground"
								/>
							</button>
						) : (
							<label htmlFor={checkboxId} className="min-w-0 flex-1 cursor-pointer">
								<span className="block truncate text-sm">{entry.name}</span>
								<span className="block truncate text-xs text-muted-foreground">
									{pickable
										? [location, meta].filter(Boolean).join(" · ")
										: t("unsupported_by", { parser: parserName })}
								</span>
							</label>
						)}
					</li>
				);
			})}
		</ul>
	);
}
