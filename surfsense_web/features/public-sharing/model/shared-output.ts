import type { PublicSharedOutput } from "@/contracts/types/public-chat.types";
import type { ArtifactManifest } from "@/features/artifacts/model/artifact";
import { publicFilePath } from "../lib/public-link-url";

/**
 * Which of the owner's viewers a shared output opens in, as the owner's own
 * Studio opens it (features/notebooks/ui/studio/output-view.tsx and
 * studio-panel.tsx): a report in its reading view with contents, an audio
 * overview in the player with its transcript, anything else in the
 * artifact viewer for its kind (mind map, flashcards, quiz, slides, table,
 * infographic, video).
 */
export type SharedOutputViewer = "report" | "audio" | "artifact";

export function sharedOutputViewer(output: Pick<PublicSharedOutput, "kind">): SharedOutputViewer {
	if (output.kind === "report") return "report";
	if (output.kind === "audio") return "audio";
	return "artifact";
}

/**
 * The shared copy in the in-app manifest's shape, so the owner's viewers draw
 * it: the text as it was shared and the artifact's files at this link's own
 * addresses. A server that sends only the main file (before ``files``) gets
 * that file alone, at the link's main-file address.
 */
export function sharedOutputManifest(token: string, output: PublicSharedOutput): ArtifactManifest {
	const files = output.files?.length
		? output.files
		: output.file
			? [
					{
						file_id: 0,
						role: "primary" as const,
						filename: output.file.filename,
						mime_type: output.file.mime_type,
						size_bytes: output.file.size_bytes,
						content_url: publicFilePath(token, output.artifact_id),
					},
				]
			: [];
	return {
		artifact_id: output.artifact_id,
		document_id: 0,
		title: output.title,
		format: output.format,
		generation: output.generation ?? 1,
		markdown_representation: output.markdown,
		files,
		updated_at: null,
	};
}
