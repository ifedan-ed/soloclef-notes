"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Download, FileText, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { useEffect, useRef, useState } from "react";
import { MarkdownViewer } from "@/components/markdown-viewer";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { selectPrimaryArtifactFile } from "@/features/artifacts/lib/artifact-selectors";
import type { ArtifactManifest } from "@/features/artifacts/model/artifact";
import {
	ArtifactRenderer,
	resolveArtifactPresentation,
} from "@/features/artifacts/rendering/artifact-renderer";
import { UnviewableFile } from "@/features/file-viewers/unviewable-file";
import { FILE_VIEWERS } from "@/features/file-viewers/viewer-registry";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { buildBackendUrl } from "@/lib/env-config";
import { cn } from "@/lib/utils";
import { liveNotebookApi } from "../../api/live-notebook-api";
import type { LiveSourceView } from "../../model/live-notebook";

export type LiveTarget =
	| { type: "artifact"; artifactId: number; title: string; label: string }
	| { type: "source"; sourceId: number; title: string; label: string; page?: number | null };

/**
 * An output or a source opened from a live notebook link, over the page: the
 * whole screen on a phone, a large panel on a desktop. The viewers are the
 * in-app ones, fed this link's manifest and file URLs; nothing here writes.
 */
export function LiveViewer({
	token,
	target,
	onClose,
}: {
	token: string;
	target: LiveTarget;
	onClose: () => void;
}) {
	const t = useTranslations("publicSharing.live");
	const titleRef = useRef<HTMLHeadingElement>(null);
	const [controls, setControls] = useState<HTMLDivElement | null>(null);

	useEffect(() => {
		titleRef.current?.focus({ preventScroll: true });
		const onKey = (event: KeyboardEvent) => {
			if (event.key === "Escape" && !event.defaultPrevented) onClose();
		};
		document.addEventListener("keydown", onKey);
		return () => document.removeEventListener("keydown", onKey);
	}, [onClose]);

	return (
		<>
			<button
				type="button"
				aria-label={t("close")}
				tabIndex={-1}
				onClick={onClose}
				className="fixed inset-0 z-40 hidden cursor-default bg-black/40 motion-safe:animate-in motion-safe:fade-in-0 lg:block"
			/>
			<section
				aria-label={target.title}
				data-testid="live-viewer"
				data-viewer={target.type}
				className="fixed inset-0 z-50 flex flex-col overflow-hidden bg-background lg:inset-4 lg:rounded-2xl lg:border lg:shadow-2xl motion-safe:lg:animate-in motion-safe:lg:fade-in-0 motion-safe:lg:zoom-in-95"
			>
				<header className="flex shrink-0 items-center gap-1 border-b px-2 py-2 pt-[max(0.5rem,env(safe-area-inset-top))]">
					<Button
						variant="ghost"
						size="icon"
						className="size-9 shrink-0 rounded-full lg:hidden"
						onClick={onClose}
						aria-label={t("back")}
					>
						<ArrowLeft className="size-4" />
					</Button>
					<div className="min-w-0 flex-1 px-1">
						<p className="truncate text-xs text-muted-foreground">{target.label}</p>
						<h2
							ref={titleRef}
							tabIndex={-1}
							className="truncate text-base font-semibold leading-snug outline-none"
						>
							{target.title}
						</h2>
					</div>
					<div ref={setControls} className="flex shrink-0 items-center gap-1 empty:hidden" />
					{target.type === "artifact" ? (
						<Button variant="ghost" size="sm" className="shrink-0 rounded-full" asChild>
							<a
								href={liveNotebookApi.downloadUrl(token, target.artifactId)}
								download
								rel="noreferrer"
								data-testid="live-download"
							>
								<Download className="size-4" />
								<span className="hidden sm:inline">{t("download")}</span>
							</a>
						</Button>
					) : null}
					<Button
						variant="ghost"
						size="icon"
						className="hidden size-9 shrink-0 rounded-full lg:inline-flex"
						onClick={onClose}
						aria-label={t("close")}
						data-testid="live-viewer-close"
					>
						<X className="size-4" />
					</Button>
				</header>
				<div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
					{target.type === "artifact" ? (
						<ArtifactBody token={token} artifactId={target.artifactId} controls={controls} />
					) : (
						<SourceBody
							token={token}
							sourceId={target.sourceId}
							page={target.page ?? undefined}
							controls={controls}
						/>
					)}
				</div>
			</section>
		</>
	);
}

function Waiting({ pending }: { pending: boolean }) {
	const t = useTranslations("publicSharing.live");
	const show = useDelayedFlag(pending);
	return (
		<div aria-busy="true" className="flex h-full items-center justify-center">
			{show ? <Spinner size="lg" /> : null}
			<span className="sr-only">{t("loading")}</span>
		</div>
	);
}

function Failed({ retry }: { retry: () => void }) {
	const t = useTranslations("publicSharing.live");
	return (
		<div role="alert" className="flex h-full flex-col items-center justify-center gap-3 p-6">
			<p className="text-sm text-muted-foreground">{t("open_failed")}</p>
			<Button variant="secondary" size="sm" onClick={retry}>
				{t("try_again")}
			</Button>
		</div>
	);
}

function ArtifactBody({
	token,
	artifactId,
	controls,
}: {
	token: string;
	artifactId: number;
	controls: HTMLElement | null;
}) {
	const manifest = useQuery({
		queryKey: ["public-notebook", token, "artifact", artifactId],
		queryFn: ({ signal }) => liveNotebookApi.manifest(token, artifactId, signal),
		retry: false,
		staleTime: 30_000,
		meta: { suppressGlobalErrorToast: true },
	});
	if (manifest.data) return <ArtifactView manifest={manifest.data} controls={controls} />;
	if (manifest.isError) return <Failed retry={() => void manifest.refetch()} />;
	return <Waiting pending />;
}

function ArtifactView({
	manifest,
	controls,
}: {
	manifest: ArtifactManifest;
	controls: HTMLElement | null;
}) {
	const primary = selectPrimaryArtifactFile(manifest);
	if (primary?.mime_type.startsWith("audio/")) {
		return (
			<div className="flex h-full items-center justify-center p-6">
				{/* biome-ignore lint/a11y/useMediaCaption: generated audio has no caption track */}
				<audio
					controls
					preload="metadata"
					src={buildBackendUrl(primary.content_url)}
					className="w-full max-w-xl"
					data-testid="live-audio"
				/>
			</div>
		);
	}
	return (
		<div className="relative min-h-0 flex-1 overflow-hidden" data-testid="live-artifact">
			<ArtifactRenderer
				workspaceId={0}
				manifest={manifest}
				zoomControlsContainer={controls}
				presentation={resolveArtifactPresentation(manifest)}
			/>
		</div>
	);
}

function SourceBody({
	token,
	sourceId,
	page,
	controls,
}: {
	token: string;
	sourceId: number;
	page?: number;
	controls: HTMLElement | null;
}) {
	const source = useQuery({
		queryKey: ["public-notebook", token, "source", sourceId],
		queryFn: ({ signal }) => liveNotebookApi.source(token, sourceId, signal),
		retry: false,
		staleTime: 30_000,
		meta: { suppressGlobalErrorToast: true },
	});
	if (source.data) return <SourceView source={source.data} page={page} controls={controls} />;
	if (source.isError) return <Failed retry={() => void source.refetch()} />;
	return <Waiting pending />;
}

function SourceView({
	source,
	page,
	controls,
}: {
	source: LiveSourceView;
	page?: number;
	controls: HTMLElement | null;
}) {
	const t = useTranslations("publicSharing.live");
	const file = source.file;
	const Viewer = file ? FILE_VIEWERS[file.mime_type] : undefined;
	if (source.presentation === "original" && file && Viewer) {
		return (
			<div className="relative min-h-0 flex-1 overflow-hidden" data-testid="live-source-file">
				<Viewer primary={file} files={[file]} zoomControlsContainer={controls} initialPage={page} />
			</div>
		);
	}
	return (
		<div className="h-full overflow-y-auto px-5 py-4" data-testid="live-source-text">
			<div className="mx-auto max-w-3xl space-y-4">
				<p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
					<FileText className="size-3.5" aria-hidden />
					{t("text_only")}
					{file ? (
						<a
							href={buildBackendUrl(file.content_url)}
							download={file.filename}
							rel="noreferrer"
							className={cn("text-primary underline-offset-4 hover:underline")}
						>
							{t("download_original")}
						</a>
					) : null}
				</p>
				{source.markdown.trim() ? (
					<MarkdownViewer content={source.markdown} className="text-sm leading-relaxed" />
				) : (
					<UnviewableFile message={t("open_failed")} />
				)}
			</div>
		</div>
	);
}
