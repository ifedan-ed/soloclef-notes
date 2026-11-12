import type { ComponentType } from "react";
import type { ArtifactManifest } from "./artifact";

export interface ArtifactRendererProps {
	manifest: ArtifactManifest;
	/**
	 * Study progress (flashcard marks and order) stays in the page instead of
	 * being saved: a public link has no account to save it to.
	 */
	localOnly?: boolean;
	zoomControlsContainer: HTMLElement | null;
}

export interface ArtifactRendererActionsProps {
	manifest: ArtifactManifest;
}

export interface ArtifactRenderer {
	Viewer: ComponentType<ArtifactRendererProps>;
	Actions?: ComponentType<ArtifactRendererActionsProps>;
	downloadable: boolean;
}
