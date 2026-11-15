"use client";

import { selectSvgPreviewFile } from "@/features/artifacts/lib/artifact-selectors";
// Not through the registry's dynamic import: this adapter is itself loaded
// on demand, so the image viewer arrives in the same chunk.
import ImageFileViewer from "@/features/file-viewers/image-file-viewer";
import type { FileViewerProps } from "@/features/file-viewers/model";

/**
 * A picture artifact, shown from its SVG copy when it has one: an
 * infographic's poster is kept as a PNG and as an SVG with its fonts embedded,
 * and the SVG stays sharp however far it is zoomed. An infographic made
 * before the SVG was kept, or any other PNG, is shown from the PNG.
 *
 * The SVG goes through <img> like the PNG, so nothing in it can run.
 */
export default function SvgPreviewImageViewer({ primary, files, ...rest }: FileViewerProps) {
	const svg = selectSvgPreviewFile(files);
	return <ImageFileViewer {...rest} primary={svg ?? primary} files={files} />;
}
