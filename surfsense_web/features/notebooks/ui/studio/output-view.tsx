"use client";

import { useQuery } from "@tanstack/react-query";
import {
	ArrowLeft,
	ChevronDown,
	ChevronRight,
	FileText,
	Globe,
	Maximize2,
	Minimize2,
	X,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { artifactDownloadPath } from "@/features/artifacts/api/artifact-download-path";
import { artifactManifestQueryOptions } from "@/features/artifacts/api/artifact-queries";
import { artifactDownloadFilename } from "@/features/artifacts/lib/artifact-selectors";
import {
	ArtifactRenderer,
	resolveArtifactPresentation,
} from "@/features/artifacts/rendering/artifact-renderer";
import { ArtifactDownloadButton } from "@/features/artifacts/ui/artifact-download-button";
import { publicLinksApi, ShareDialog, useCanCreatePublicLinks } from "@/features/public-sharing";
import { cn } from "@/lib/utils";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { useOutputView } from "../../hooks/use-output-view";
import { type OutputSize, outputSize, rememberOutputSize } from "../../lib/output-size";
import type { StudioOutput } from "../../model/notebook";
import { ExplainHostProvider } from "../../state/explain";
import { ReportBody } from "../report/report-viewer";

const DESKTOP = "(min-width: 1024px)";

/** Read synchronously, so the first paint already has the right size (no jump). */
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
 * An open Studio output, as NotebookLM shows one: "Studio › Quiz", the title,
 * a "View prompt and N sources" chip, the viewer, and Good / Bad content.
 *
 * On a desktop it has two sizes, toggled by the expand icon and remembered
 * per output for the session: inside the widened Studio column, with the chat
 * usable beside it, or the whole window. Reports open expanded the first
 * time. "Explain" from an expanded output drops it back into the column, at
 * the same place, so the answer streams in the chat beside it. On a phone
 * the output fills the screen over the Studio tab; Explain switches to the
 * Chat tab and leaves it mounted, so coming back finds it where it was.
 */
export function OutputView({
	workspaceId,
	notebookId,
	output,
	onClose,
}: {
	workspaceId: number;
	notebookId: number;
	output: StudioOutput;
	onClose: () => void;
}) {
	const t = useTranslations("notebooks.output_view");
	const tn = useTranslations("notebooks");
	const isDesktop = useIsDesktop();
	const [size, setSizeState] = useState<OutputSize>(() => outputSize(output.id, output.kind));
	const setSize = useCallback(
		(next: OutputSize) => {
			rememberOutputSize(output.id, next);
			setSizeState(next);
		},
		[output.id]
	);
	const expanded = isDesktop && size === "expanded";
	const [details, setDetails] = useState(false);
	const [controls, setControls] = useState<HTMLDivElement | null>(null);
	const titleRef = useRef<HTMLHeadingElement>(null);
	const { view } = useOutputView(workspaceId, notebookId, output.id);
	const isReport = output.kind === "report";
	const tShare = useTranslations("publicSharing");
	const canShare = useCanCreatePublicLinks() && output.status === "ready";
	const [shareOpen, setShareOpen] = useState(false);

	useEffect(() => {
		titleRef.current?.focus({ preventScroll: true });
	}, []);

	useEffect(() => {
		if (!expanded) return;
		const onKey = (event: KeyboardEvent) => {
			if (event.key === "Escape" && !event.defaultPrevented) setSize("column");
		};
		document.addEventListener("keydown", onKey);
		return () => document.removeEventListener("keydown", onKey);
	}, [expanded, setSize]);

	const onExplain = useCallback(() => {
		if (expanded) setSize("column");
	}, [expanded, setSize]);

	const sourceCount = view.data?.sources.length ?? output.source_count;
	const kindLabel = tn(`kinds.${output.kind}.label`);

	return (
		<>
			{canShare ? (
				<ShareDialog
					open={shareOpen}
					onOpenChange={setShareOpen}
					workspaceId={workspaceId}
					subject="output"
					itemTitle={output.title}
					create={(expiry) =>
						publicLinksApi.shareOutput(workspaceId, notebookId, output.id, expiry)
					}
				/>
			) : null}
			{size === "expanded" ? (
				<button
					type="button"
					aria-label={t("collapse")}
					tabIndex={-1}
					onClick={() => setSize("column")}
					className="fixed inset-0 z-40 hidden cursor-default bg-black/40 motion-safe:animate-in motion-safe:fade-in-0 lg:block"
				/>
			) : null}
			<section
				aria-label={output.title}
				data-output-view={output.kind}
				data-output-size={isDesktop ? size : "screen"}
				className={cn(
					"flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-panel",
					// A phone: the whole screen, over the Studio tab.
					"max-lg:fixed max-lg:inset-0 max-lg:z-50",
					size === "expanded" &&
						"lg:fixed lg:inset-3 lg:z-50 lg:rounded-2xl lg:border lg:bg-background lg:shadow-2xl motion-safe:lg:animate-in motion-safe:lg:fade-in-0 motion-safe:lg:zoom-in-95"
				)}
			>
				<header className="flex shrink-0 items-center gap-1 px-2 pt-2 max-lg:pt-[max(0.5rem,env(safe-area-inset-top))]">
					<Button
						variant="ghost"
						size="icon"
						className="size-9 shrink-0 rounded-full lg:hidden"
						onClick={onClose}
						aria-label={t("back")}
					>
						<ArrowLeft className="size-4" />
					</Button>
					<nav
						aria-label={t("studio")}
						className="flex min-w-0 flex-1 items-center gap-1 px-1 text-sm text-muted-foreground"
					>
						<button
							type="button"
							onClick={onClose}
							className="shrink-0 rounded hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
						>
							{t("studio")}
						</button>
						<ChevronRight className="size-3.5 shrink-0" aria-hidden />
						<span className="truncate text-foreground" aria-current="page">
							{kindLabel}
						</span>
					</nav>
					<div ref={setControls} className="flex shrink-0 items-center gap-1 empty:hidden" />
					{canShare ? (
						<Button
							variant="ghost"
							size="icon"
							className="size-8 shrink-0 rounded-full text-muted-foreground"
							onClick={() => setShareOpen(true)}
							aria-label={tShare("menu_share_output")}
							title={tShare("menu_share_output")}
							data-testid="output-share"
						>
							<Globe className="size-4" />
						</Button>
					) : null}
					{isReport ? null : (
						<ArtifactActions workspaceId={workspaceId} artifactId={output.artifact_id ?? 0} />
					)}
					<Button
						variant="ghost"
						size="icon"
						className="hidden size-8 shrink-0 rounded-full text-muted-foreground lg:inline-flex"
						onClick={() => setSize(expanded ? "column" : "expanded")}
						aria-label={expanded ? t("collapse") : t("expand")}
						aria-pressed={expanded}
						data-testid="output-size-toggle"
					>
						{expanded ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
					</Button>
					<Button
						variant="ghost"
						size="icon"
						className="hidden size-8 shrink-0 rounded-full text-muted-foreground lg:inline-flex"
						onClick={onClose}
						aria-label={t("close")}
					>
						<X className="size-4" />
					</Button>
				</header>
				<div className="shrink-0 px-4 pb-3">
					<h2
						ref={titleRef}
						tabIndex={-1}
						className="break-words text-lg font-semibold leading-snug outline-none"
					>
						{output.title}
					</h2>
					<Button
						variant="outline"
						size="sm"
						className="mt-2 h-7 max-w-full rounded-full px-3 text-xs"
						aria-expanded={details}
						onClick={() => setDetails((open) => !open)}
					>
						<FileText className="size-3.5" />
						<span className="truncate">{t("view_prompt", { count: sourceCount })}</span>
						<ChevronDown
							className={cn("size-3.5 transition-transform", details && "rotate-180")}
							aria-hidden
						/>
					</Button>
					{details ? (
						<section
							aria-label={t("view_prompt", { count: sourceCount })}
							className="mt-2 max-h-56 space-y-3 overflow-y-auto rounded-xl border bg-muted/30 p-3 text-sm"
						>
							<div>
								<p className="mb-1 text-xs font-medium text-muted-foreground">
									{t("prompt_title")}
								</p>
								<p className="whitespace-pre-wrap break-words">
									{view.data ? view.data.prompt || t("no_prompt") : t("loading")}
								</p>
							</div>
							<div>
								<p className="mb-1 text-xs font-medium text-muted-foreground">
									{t("sources_title")}
								</p>
								{view.data?.sources.length ? (
									<ul className="space-y-1">
										{view.data.sources.map((source) => (
											<li key={source.id} className="flex min-w-0 items-start gap-2">
												<FileText
													className="mt-0.5 size-4 shrink-0 text-muted-foreground"
													aria-hidden
												/>
												<span className="min-w-0 break-words">{source.title}</span>
											</li>
										))}
									</ul>
								) : (
									<p className="text-muted-foreground">
										{view.data ? t("no_sources") : t("loading_sources")}
									</p>
								)}
							</div>
						</section>
					) : null}
				</div>
				<div className="relative flex min-h-0 flex-1 flex-col overflow-hidden border-t">
					<ExplainHostProvider onExplain={onExplain}>
						{isReport ? (
							<ReportBody
								workspaceId={workspaceId}
								notebookId={notebookId}
								output={output}
								compact={!expanded}
								controls={controls}
							/>
						) : (
							<ArtifactBody
								workspaceId={workspaceId}
								artifactId={output.artifact_id ?? 0}
								controls={controls}
							/>
						)}
					</ExplainHostProvider>
				</div>
			</section>
		</>
	);
}

function useManifest(workspaceId: number, artifactId: number) {
	return useQuery({
		...artifactManifestQueryOptions(workspaceId, artifactId),
		enabled: workspaceId > 0 && artifactId > 0,
	});
}

/** The viewer's own header actions (a deck's reset) and its download. */
function ArtifactActions({ workspaceId, artifactId }: { workspaceId: number; artifactId: number }) {
	const manifest = useManifest(workspaceId, artifactId);
	if (!manifest.data) return null;
	const { renderer } = resolveArtifactPresentation(manifest.data);
	const Actions = renderer.Actions;
	return (
		<>
			{Actions ? <Actions workspaceId={workspaceId} manifest={manifest.data} /> : null}
			{renderer.downloadable ? (
				<ArtifactDownloadButton
					path={artifactDownloadPath(workspaceId, artifactId)}
					filename={artifactDownloadFilename(manifest.data, artifactId)}
					className="size-8 shrink-0 rounded-full text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
				/>
			) : null}
		</>
	);
}

function ArtifactBody({
	workspaceId,
	artifactId,
	controls,
}: {
	workspaceId: number;
	artifactId: number;
	controls: HTMLElement | null;
}) {
	const t = useTranslations("notebooks.output_view");
	const manifest = useManifest(workspaceId, artifactId);
	const showSpinner = useDelayedFlag(manifest.isPending);
	if (manifest.data) {
		return (
			<div className="relative min-h-0 flex-1 overflow-hidden">
				<ArtifactRenderer
					workspaceId={workspaceId}
					manifest={manifest.data}
					zoomControlsContainer={controls}
					presentation={resolveArtifactPresentation(manifest.data)}
				/>
			</div>
		);
	}
	if (manifest.isError) {
		return (
			<div
				role="alert"
				className="flex h-full flex-col items-center justify-center gap-3 p-6 text-center"
			>
				<p className="text-sm text-muted-foreground">{t("open_failed")}</p>
				<Button variant="secondary" size="sm" onClick={() => void manifest.refetch()}>
					{t("try_again")}
				</Button>
			</div>
		);
	}
	return (
		<div aria-busy="true" className="flex h-full items-center justify-center">
			{showSpinner ? <Spinner size="lg" /> : null}
			<span className="sr-only">{t("loading")}</span>
		</div>
	);
}
