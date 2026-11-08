import type { StudioOutput } from "../model/notebook";

export type StudioOpenAction =
	| { type: "audio-player"; artifactId: number }
	| { type: "studio-view"; outputId: number; artifactId: number }
	| null;

/**
 * What opening a Studio output does. Audio plays in the Studio's own player,
 * which keeps its place while the user reads the notebook; everything else,
 * reports and video included, opens in the Studio's output view
 * (ui/studio/output-view), which picks its viewer by the artifact's format
 * and primary file (an MP4 plays in the native video player).
 */
export function studioOpenAction(output: StudioOutput): StudioOpenAction {
	if (output.status !== "ready" || !output.artifact_id) return null;
	if (output.kind === "audio") return { type: "audio-player", artifactId: output.artifact_id };
	return { type: "studio-view", outputId: output.id, artifactId: output.artifact_id };
}
