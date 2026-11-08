import { fileExtension } from "@/lib/etl/parser-choice";
import type { PickedItem, RemoteEntry } from "../api/nextcloud-files-api";

export type Crumb = { name: string; path: string };

/** The folders from the root down to ``path``; the root's name is left to the caller. */
export function breadcrumbs(path: string): Crumb[] {
	const parts = path.split("/").filter(Boolean);
	return [
		{ name: "", path: "/" },
		...parts.map((name, index) => ({ name, path: `/${parts.slice(0, index + 1).join("/")}` })),
	];
}

export function parentPath(path: string): string {
	const parts = path.split("/").filter(Boolean);
	return parts.length > 1 ? `/${parts.slice(0, -1).join("/")}` : "/";
}

/** Folders can always be picked; a file only when the chosen parser reads its type. */
export function isPickable(entry: RemoteEntry, accepted: Set<string> | null): boolean {
	if (entry.is_folder) return true;
	if (!accepted) return false;
	return accepted.has(fileExtension(entry.name));
}

export type Selection = ReadonlyMap<string, PickedItem>;

export function toggleEntry(selection: Selection, entry: RemoteEntry): Map<string, PickedItem> {
	const next = new Map(selection);
	if (next.has(entry.path)) next.delete(entry.path);
	else next.set(entry.path, { path: entry.path, kind: entry.is_folder ? "folder" : "file" });
	return next;
}

/** Tick every pickable entry shown, or clear them all when they are already ticked. */
export function toggleAll(
	selection: Selection,
	entries: RemoteEntry[],
	accepted: Set<string> | null
): Map<string, PickedItem> {
	const pickable = entries.filter((entry) => isPickable(entry, accepted));
	const allPicked = pickable.length > 0 && pickable.every((entry) => selection.has(entry.path));
	const next = new Map(selection);
	for (const entry of pickable) {
		if (allPicked) next.delete(entry.path);
		else next.set(entry.path, { path: entry.path, kind: entry.is_folder ? "folder" : "file" });
	}
	return next;
}

/** Drop picked files the new parser choice cannot read. */
export function withoutUnreadable(
	selection: Selection,
	accepted: Set<string>
): Map<string, PickedItem> {
	const next = new Map<string, PickedItem>();
	for (const [path, item] of selection) {
		if (item.kind === "folder" || accepted.has(fileExtension(path))) next.set(path, item);
	}
	return next;
}

export function countPicked(selection: Selection): { files: number; folders: number } {
	let files = 0;
	let folders = 0;
	for (const item of selection.values()) {
		if (item.kind === "folder") folders += 1;
		else files += 1;
	}
	return { files, folders };
}

export function formatBytes(bytes: number | null): string {
	if (bytes === null) return "";
	if (bytes < 1024) return `${bytes} B`;
	const units = ["KB", "MB", "GB", "TB"];
	let value = bytes / 1024;
	let unit = 0;
	while (value >= 1024 && unit < units.length - 1) {
		value /= 1024;
		unit += 1;
	}
	return `${value >= 10 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

export type FileKind =
	| "folder"
	| "pdf"
	| "text"
	| "sheet"
	| "slides"
	| "image"
	| "audio"
	| "video"
	| "other";

const KINDS: Record<string, FileKind> = {
	".pdf": "pdf",
	".doc": "text",
	".docx": "text",
	".odt": "text",
	".rtf": "text",
	".txt": "text",
	".md": "text",
	".html": "text",
	".epub": "text",
	".xls": "sheet",
	".xlsx": "sheet",
	".ods": "sheet",
	".csv": "sheet",
	".tsv": "sheet",
	".ppt": "slides",
	".pptx": "slides",
	".odp": "slides",
	".png": "image",
	".jpg": "image",
	".jpeg": "image",
	".gif": "image",
	".webp": "image",
	".heic": "image",
	".svg": "image",
	".mp3": "audio",
	".wav": "audio",
	".m4a": "audio",
	".ogg": "audio",
	".flac": "audio",
	".mp4": "video",
	".mkv": "video",
	".mov": "video",
	".webm": "video",
	".avi": "video",
};

export function fileKind(entry: RemoteEntry): FileKind {
	if (entry.is_folder) return "folder";
	return KINDS[fileExtension(entry.name)] ?? "other";
}
