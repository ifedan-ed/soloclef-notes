import type { PickedDriveFile } from "../api/google-drive-api";

export const DRIVE_FOLDER_MIME = "application/vnd.google-apps.folder";

/** What the Picker hands back for one document, as plain fields. */
export type PickerDoc = { id?: string; name?: string; mimeType?: string };

/**
 * The files among a Picker selection. Folders are dropped: under drive.file
 * picking a folder does not give access to what is inside it.
 */
export function pickedFiles(docs: PickerDoc[]): PickedDriveFile[] {
	const seen = new Set<string>();
	const files: PickedDriveFile[] = [];
	for (const doc of docs) {
		if (!doc.id || seen.has(doc.id) || doc.mimeType === DRIVE_FOLDER_MIME) continue;
		seen.add(doc.id);
		files.push({ id: doc.id, name: doc.name?.trim() || "Untitled", mime_type: doc.mimeType ?? "" });
	}
	return files;
}

/** The app path to come back to after Google, never another origin. */
export function returnPathFrom(location: { pathname: string; search: string }): string {
	const params = new URLSearchParams(location.search);
	params.delete("google_drive");
	const query = params.toString();
	return `${location.pathname}${query ? `?${query}` : ""}`;
}
