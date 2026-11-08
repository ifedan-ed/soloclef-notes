/**
 * What the panel's Save goes by after the editor reports a change: the
 * edited text, or null when it is the saved text again; `undefined` when
 * the change is not the person's (the panel is not editing, or the file is
 * still loading). The editor reports edits only, the first one included
 * (PlateEditor's onMarkdownChange). The panel used to skip the first change
 * it was told of, which was then the editor's own, on opening: once the
 * editor stopped reporting that, the skip swallowed the first edit and left
 * Save off after it.
 */
export function editAfterChange(
	markdown: string,
	panel: { editing: boolean; loaded: boolean; saved: string }
): string | null | undefined {
	if (!panel.editing || !panel.loaded) return undefined;
	return markdown === panel.saved ? null : markdown;
}
