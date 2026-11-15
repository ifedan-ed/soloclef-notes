// Puts rendered Markdown on the clipboard as both text/html and text/plain.
//
// Two ways, because browsers differ:
//
//  1. navigator.clipboard.write with a ClipboardItem (Chrome, Edge, Safari
//     13.1+, Firefox 127+). The item is handed promises, and written at
//     once, inside the click: Safari refuses a clipboard write that starts
//     after an await, and the content may still be waiting on the sources'
//     titles.
//  2. Otherwise, or when that is refused, the copy event: document.execCommand
//     ("copy") fires it synchronously and its handler sets both types itself.
//     That works in older Safari and Firefox and outside a secure context,
//     where navigator.clipboard is missing. It has to run while the click
//     still counts as a user action, which is why callers fetch titles ahead
//     of the click where they can (useMarkdownCopy's prefetch).

import type { ClipboardFormats } from "./clipboard-formats";

/** What the writer needs from the browser: injectable, so it can be tested. */
export interface ClipboardHost {
	clipboard?: Pick<Clipboard, "write">;
	ClipboardItem?: typeof ClipboardItem;
	/** Copies both forms with the copy command; false when the browser refuses it. */
	execCopy: (formats: ClipboardFormats) => boolean;
}

/**
 * Write `content` (or the promise of it) to the clipboard. Resolves to
 * whether it was written.
 */
export async function writeClipboard(
	content: ClipboardFormats | Promise<ClipboardFormats>,
	host: ClipboardHost = browserHost()
): Promise<boolean> {
	const ready = Promise.resolve(content);
	if (host.clipboard && host.ClipboardItem) {
		try {
			const blob = (type: "text/html" | "text/plain") =>
				ready.then(
					(formats) => new Blob([type === "text/html" ? formats.html : formats.plain], { type })
				);
			await host.clipboard.write([
				new host.ClipboardItem({
					"text/html": blob("text/html"),
					"text/plain": blob("text/plain"),
				}),
			]);
			return true;
		} catch {
			// Refused (a browser that takes no promises in an item, a
			// permission policy): the copy event below may still be allowed.
		}
	}
	let formats: ClipboardFormats;
	try {
		formats = await ready;
	} catch {
		return false;
	}
	return host.execCopy(formats);
}

function browserHost(): ClipboardHost {
	return {
		clipboard:
			typeof navigator !== "undefined" && window.isSecureContext ? navigator.clipboard : undefined,
		ClipboardItem: typeof ClipboardItem === "undefined" ? undefined : ClipboardItem,
		execCopy: (formats) => {
			const onCopy = (event: ClipboardEvent) => {
				if (!event.clipboardData) return;
				event.preventDefault();
				event.clipboardData.setData("text/html", formats.html);
				event.clipboardData.setData("text/plain", formats.plain);
			};
			// The plain text is selected too: some browsers fire the copy
			// event only with a selection, and one that fires it without
			// clipboardData still copies the selected (clean) text.
			const focused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
			const area = document.createElement("textarea");
			area.value = formats.plain;
			area.setAttribute("readonly", "");
			area.style.position = "fixed";
			area.style.top = "0";
			area.style.left = "0";
			area.style.opacity = "0";
			document.body.appendChild(area);
			area.select();
			document.addEventListener("copy", onCopy);
			try {
				return document.execCommand("copy");
			} catch {
				return false;
			} finally {
				// Removed whether or not the event fired, so a refused
				// command does not hijack the person's next Ctrl+C.
				document.removeEventListener("copy", onCopy);
				area.remove();
				// The Copy button keeps the focus, as it would after a click.
				focused?.focus();
			}
		},
	};
}
