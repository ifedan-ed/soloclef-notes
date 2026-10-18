"use client";

import { ChevronRight } from "lucide-react";
import { useTranslations } from "next-intl";
import { useMemo, useState, useSyncExternalStore } from "react";
import { PodcastPlayer } from "@/components/tool-ui/podcast/player";
import type { PublicSharedOutput } from "@/contracts/types/public-chat.types";
import { artifactDownloadFilename } from "@/features/artifacts/lib/artifact-selectors";
import {
	ArtifactRenderer,
	resolveArtifactPresentation,
} from "@/features/artifacts/rendering/artifact-renderer";
import { ArtifactDownloadButton } from "@/features/artifacts/ui/artifact-download-button";
import { ReportBody } from "@/features/notebooks/ui/report/report-viewer";
import { sharedOutputManifest, sharedOutputViewer } from "../model/shared-output";
import { SharedNotice } from "./shared-notice";

const DESKTOP = "(min-width: 1024px)";

/** Read synchronously, so a report's contents are where they belong on the first paint. */
function useIsDesktop(): boolean {
	return useSyncExternalStore(
		(onChange) => {
			const query = window.matchMedia(DESKTOP);
			query.addEventListener("change", onChange);
			return () => query.removeEventListener("change", onChange);
		},
		() => window.matchMedia(DESKTOP).matches,
		() => false
	);
}

/**
 * A shared Studio output, read-only, as its owner sees it in the Studio
 * (features/notebooks/ui/studio/output-view.tsx): its kind, its title, then
 * the owner's own viewer for that kind, fed the link's copy. A mind map is
 * the interactive map, flashcards and a quiz are studied on the page (the
 * visitor's answers stay in their browser), a deck shows its slides, a data
 * table its sheet, a report its reading view with contents, an audio
 * overview its player and transcript. Nothing here writes, and the parts
 * that act on the owner's notebook (the prompt and sources, Explain, report
 * suggestions, Share) are not shown.
 */
export function PublicOutputView({
	shareToken,
	output,
	expiresAt,
}: {
	shareToken: string;
	output: PublicSharedOutput;
	expiresAt: string | null | undefined;
}) {
	const t = useTranslations("publicSharing.live");
	const tKinds = useTranslations("notebooks.kinds");
	const kindKey = `${output.kind}.label`;
	const kindLabel = tKinds.has(kindKey) ? tKinds(kindKey) : output.format;
	const manifest = useMemo(() => sharedOutputManifest(shareToken, output), [shareToken, output]);
	const presentation = useMemo(() => resolveArtifactPresentation(manifest), [manifest]);
	const viewer = sharedOutputViewer(output);
	const isDesktop = useIsDesktop();
	const [controls, setControls] = useState<HTMLDivElement | null>(null);
	const primary = presentation.primary;
	// The owner's header Download, for the kinds the owner can download (not flashcards or a quiz).
	const download =
		viewer === "artifact" && primary && presentation.renderer.downloadable ? primary : null;

	return (
		<section
			aria-label={output.title}
			data-output-view={output.kind}
			data-testid="public-output"
			className="flex min-h-0 flex-1 flex-col pt-16"
		>
			<header className="flex shrink-0 items-center gap-1 px-2 pt-2">
				<p className="flex min-w-0 flex-1 items-center gap-1 px-2 text-sm text-muted-foreground">
					<span className="shrink-0">{t("studio")}</span>
					<ChevronRight className="size-3.5 shrink-0" aria-hidden />
					<span className="truncate text-foreground" data-testid="public-output-kind">
						{kindLabel}
					</span>
				</p>
				<div ref={setControls} className="flex shrink-0 items-center gap-1 empty:hidden" />
				{download ? (
					<ArtifactDownloadButton
						path={download.content_url}
						filename={artifactDownloadFilename(manifest, output.artifact_id)}
						className="size-8 shrink-0 rounded-full text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
					/>
				) : null}
			</header>
			<div className="shrink-0 px-4 pb-3">
				<h1
					className="break-words text-lg font-semibold leading-snug"
					data-testid="public-output-title"
				>
					{output.title}
				</h1>
				<SharedNotice expiresAt={expiresAt} className="mt-1 text-xs text-muted-foreground" />
			</div>
			<div className="relative flex min-h-0 flex-1 flex-col overflow-hidden border-t">
				{viewer === "report" ? (
					<ReportBody
						workspaceId={0}
						notebookId={0}
						output={{ id: 0, title: output.title, artifact_id: output.artifact_id, options: {} }}
						compact={!isDesktop}
						controls={controls}
						shared={{ manifest }}
					/>
				) : viewer === "audio" && primary ? (
					<div className="h-full overflow-y-auto px-3" data-testid="public-output-audio">
						<PodcastPlayer
							artifactId={output.artifact_id}
							title={output.title}
							transcriptMarkdown={output.markdown}
						/>
					</div>
				) : (
					<div className="relative min-h-0 flex-1 overflow-hidden" data-testid="public-artifact">
						<ArtifactRenderer
							workspaceId={0}
							manifest={manifest}
							zoomControlsContainer={controls}
							presentation={presentation}
						/>
					</div>
				)}
			</div>
		</section>
	);
}
