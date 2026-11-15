"use client";

import { useCallback, useMemo, useRef } from "react";
import { type ClipboardFormats, clipboardFormats, numberingForCopy } from "./clipboard-formats";
import { namedSources, type SourceNamer } from "./source-namer";
import { useSourceNamer } from "./source-namer-context";
import { writeClipboard } from "./write-clipboard";

/**
 * The clipboard and download forms of `texts`, its sources named by `namer`.
 * For a caller whose text is only known at the click (the editor panel's,
 * which follows the edits); a component with its text at hand uses the hook.
 */
export async function buildClipboardFormats(
	texts: readonly string[],
	namer: SourceNamer
): Promise<ClipboardFormats> {
	const numbering = numberingForCopy(texts);
	return clipboardFormats(texts, numbering, await namedSources(numbering, namer));
}

/**
 * Copy and download for one piece of rendered Markdown (an answer's text
 * parts, a note, a document).
 *
 * `prefetch` reads the sources' names ahead of a click (call it on hover or
 * focus of the button): the clipboard fallback for Firefox and older Safari
 * must write while the click still counts as the person's, so the titles
 * should already be here. The formats are built once per text and reused by
 * a second copy.
 */
export function useMarkdownCopy(texts: readonly string[]) {
	const namer = useSourceNamer();
	// A new array of the same words (each render of a message builds one)
	// must not throw the built formats away.
	const signature = texts.join("\u0000");
	const formats = useRef<{ signature: string; built: Promise<ClipboardFormats> } | null>(null);

	const build = useCallback((): Promise<ClipboardFormats> => {
		if (formats.current?.signature === signature) return formats.current.built;
		const parts = signature === "" ? [] : signature.split("\u0000");
		const built = buildClipboardFormats(parts, namer);
		formats.current = { signature, built };
		built.catch(() => {
			if (formats.current?.built === built) formats.current = null;
		});
		return built;
	}, [signature, namer]);

	return useMemo(
		() => ({
			prefetch: () => void build().catch(() => {}),
			/** Copies html and clean plain text; resolves to whether it worked. */
			copy: () => writeClipboard(build()),
			/** Saves the Markdown, with numbered citations and its sources, as `filename`. */
			download: async (filename: string) => {
				const { markdown } = await build();
				const url = URL.createObjectURL(
					new Blob([markdown], { type: "text/markdown;charset=utf-8" })
				);
				const anchor = document.createElement("a");
				anchor.href = url;
				anchor.download = filename;
				anchor.click();
				// Revoked after the click has been handled, not during it.
				window.setTimeout(() => URL.revokeObjectURL(url), 0);
			},
		}),
		[build]
	);
}
