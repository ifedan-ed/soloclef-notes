// ---------------------------------------------------------------------------
// What an editor tells its owner as the person edits
// ---------------------------------------------------------------------------
// The editor never writes a text back byte for byte as it read it: it
// escapes ("\[citation:12]", "\<"), changes list marks, pads tables and adds
// an empty paragraph at the end. Reporting that on any change (a click, a
// selection, the editor tidying its own tree) made the notes' autosave
// rewrite a note that was only opened, and a note document's Save turn on
// by itself. So nothing is reported until the text differs from what was
// loaded, and an edit undone reports the loaded Markdown as it was written.
// ---------------------------------------------------------------------------

/** Trailing white space and the zero-width space of an empty last paragraph. */
const TRAILING_BLANK = /[\s\u200b\ufeff]+$/u;

function settled(markdown: string): string {
	return markdown.replace(TRAILING_BLANK, "");
}

/**
 * `loaded` is the Markdown the editor was given; `loadedAsWritten` is the
 * editor's own writing of it, before any edit. The function returned takes
 * the editor's Markdown after each change and gives what to report, or null
 * when there is nothing new to report.
 */
export function editedMarkdownReporter(
	loaded: string,
	loadedAsWritten: string
): (written: string) => string | null {
	const unedited = settled(loadedAsWritten);
	let last = loaded;
	return (written) => {
		const next = settled(written) === unedited ? loaded : written;
		if (next === last) return null;
		last = next;
		return next;
	};
}
