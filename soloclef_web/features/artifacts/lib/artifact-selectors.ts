import type { ArtifactFile, ArtifactManifest } from "@/features/artifacts/model/artifact";
import { extension } from "@/features/file-viewers/file-format";

export function selectPrimaryArtifactFile(
	manifest: ArtifactManifest | null | undefined
): ArtifactFile | undefined {
	return manifest?.files.find((file) => file.role === "primary");
}

const SVG = "image/svg+xml";

/**
 * The vector copy of a picture artifact: an infographic's poster as SVG, kept
 * beside its PNG with the fonts embedded, so it draws exactly in a browser and
 * stays sharp at any zoom. Older infographics have only the PNG.
 */
export function selectSvgPreviewFile<T extends { role?: string; mime_type: string }>(
	files: readonly T[]
): T | undefined {
	return files.find((file) => file.role === "preview" && file.mime_type === SVG);
}

export function artifactDownloadFilename(
	manifest: ArtifactManifest | null | undefined,
	artifactId: number
): string {
	return (
		selectPrimaryArtifactFile(manifest)?.filename ??
		(manifest ? `${manifest.title}.md` : `artifact-${artifactId}`)
	);
}

/** One file an artifact can be downloaded as: its format's name, where, and as what. */
export interface ArtifactDownload {
	/** "PNG", "SVG": what the menu offers it as. */
	label: string;
	path: string;
	filename: string;
}

/**
 * What an artifact can be downloaded as. The main file always, from
 * `primaryPath` (the owner's download route, or a public link's own file
 * address); and an SVG kept beside a picture, which is a copy of the same
 * picture worth having on its own (it scales and edits). A PDF made only to
 * preview a deck or a Word file is not offered: it is a viewing aid, and the
 * deck or document itself is the download.
 */
export function artifactDownloads(
	manifest: ArtifactManifest,
	artifactId: number,
	primaryPath: string
): ArtifactDownload[] {
	const filename = artifactDownloadFilename(manifest, artifactId);
	const downloads: ArtifactDownload[] = [
		{ label: extension(filename), path: primaryPath, filename },
	];
	const svg = selectSvgPreviewFile(manifest.files);
	if (svg) downloads.push({ label: "SVG", path: svg.content_url, filename: svg.filename });
	return downloads;
}
