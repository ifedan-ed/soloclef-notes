import type { DriveImportRequest, PickedDriveFile } from "../api/google-drive-api";

export const DRIVE_FOLDER_MIME = "application/vnd.google-apps.folder";

/**
 * The most files one import takes; the server's MaxPickedFiles
 * (soloclef_api/internal/drivesync) refuses the whole request above it.
 */
export const MAX_PICKED_FILES = 50;

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

/**
 * Why a selection cannot be sent, or null when it can. The server takes 1 to
 * MAX_PICKED_FILES files and answers anything else with a 422, which would
 * reach the person as "try again" although trying again never helps. Too many
 * is refused rather than cut down: quietly importing the first fifty would
 * leave files out without saying so.
 */
export function pickProblem(files: PickedDriveFile[]): "none" | "too_many" | null {
	if (files.length === 0) return "none";
	if (files.length > MAX_PICKED_FILES) return "too_many";
	return null;
}

/**
 * The import request for a selection. The notebook is a named field because
 * it is a plain number, as is the connector id beside it at the call: by
 * position, swapping the two would still compile and would file the picks in
 * the wrong notebook.
 */
export function driveImportRequest({
	notebookId,
	files,
}: {
	notebookId: number | null;
	files: PickedDriveFile[];
}): DriveImportRequest {
	return { files, notebook_id: notebookId };
}

/** The app path to come back to after Google, never another origin. */
export function returnPathFrom(location: { pathname: string; search: string }): string {
	const params = new URLSearchParams(location.search);
	params.delete("google_drive");
	const query = params.toString();
	return `${location.pathname}${query ? `?${query}` : ""}`;
}
