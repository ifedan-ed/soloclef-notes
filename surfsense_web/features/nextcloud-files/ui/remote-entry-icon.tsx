import {
	File,
	FileAudio,
	FileImage,
	FileSpreadsheet,
	FileText,
	FileVideo,
	Folder,
	Presentation,
} from "lucide-react";
import type { RemoteEntry } from "../api/nextcloud-files-api";
import { type FileKind, fileKind } from "../lib/picker-selection";

const ICONS: Record<FileKind, typeof File> = {
	folder: Folder,
	pdf: FileText,
	text: FileText,
	sheet: FileSpreadsheet,
	slides: Presentation,
	image: FileImage,
	audio: FileAudio,
	video: FileVideo,
	other: File,
};

export function RemoteEntryIcon({ entry }: { entry: RemoteEntry }) {
	const kind = fileKind(entry);
	const Icon = ICONS[kind];
	return (
		<Icon
			aria-hidden="true"
			className={`size-4 shrink-0 ${kind === "folder" ? "text-sky-600 dark:text-sky-400" : "text-muted-foreground"}`}
		/>
	);
}
