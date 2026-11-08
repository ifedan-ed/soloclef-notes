/**
 * Whether the editor's text differs from what was saved. The editor also
 * reports its first, unedited render, which may add trailing spaces or blank
 * lines; those alone are not an edit.
 */
export function isUnsavedEdit(draft: string | null, saved: string | undefined): boolean {
	if (draft === null || saved === undefined) return false;
	const tidy = (text: string) =>
		text
			.split("\n")
			.map((line) => line.trimEnd())
			.join("\n")
			.trim();
	return tidy(draft) !== tidy(saved);
}
