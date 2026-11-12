import { NOTES_PAGE } from "@/lib/dashboard-paths";

/** One note's address in the Notes place. */
export function notePath(noteId: number): string {
	return `${NOTES_PAGE}/${noteId}`;
}

/** The note an address names, or null for the list itself (or any other page). */
export function noteIdFromPath(pathname: string | null | undefined): number | null {
	if (!pathname?.startsWith(`${NOTES_PAGE}/`)) return null;
	const segment = pathname.slice(NOTES_PAGE.length + 1).split("/")[0];
	if (!/^\d+$/.test(segment)) return null;
	const id = Number(segment);
	return Number.isSafeInteger(id) && id > 0 ? id : null;
}
