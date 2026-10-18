"use client";

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { FileText, Globe } from "lucide-react";
import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { PublicHeader } from "@/components/site/public-header";
import { Spinner } from "@/components/ui/spinner";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { STUDIO_TILES } from "@/features/notebooks/lib/studio-catalog";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { cn } from "@/lib/utils";
import { LiveLinkError, liveNotebookApi } from "../../api/live-notebook-api";
import { publicLinkFailure } from "../../lib/public-link-failure";
import type { LiveCitation, LiveNotebook, LiveOutput, LiveSource } from "../../model/live-notebook";
import { PublicLinkUnavailable } from "../public-link-unavailable";
import { SharedNotice } from "../shared-notice";
import { LiveChat } from "./live-chat";
import { type LiveTarget, LiveViewer } from "./live-viewer";

/** How often an open page asks for the notebook again (only while visible). */
export const LIVE_REFRESH_MS = 8_000;

type Tab = "sources" | "chat" | "studio";

/**
 * A notebook shared through a live link, read-only and signed out: its chat,
 * its finished Studio outputs and its sources, as they are now. The page
 * re-reads the notebook every few seconds while it is open, keeping what is
 * painted until the new read arrives, so a new answer or a finished output
 * simply appears. An expired or revoked link turns into the unavailable page.
 */
export function LiveNotebookPage({ token }: { token: string }) {
	const t = useTranslations("publicSharing.live");
	const [tab, setTab] = useState<Tab>("chat");
	const [target, setTarget] = useState<LiveTarget | null>(null);
	const page = useQuery({
		queryKey: cacheKeys.publicNotebook.byToken(token),
		queryFn: ({ signal }) => liveNotebookApi.page(token, signal),
		refetchInterval: (query) =>
			query.state.error instanceof LiveLinkError && query.state.error.status < 500
				? false
				: LIVE_REFRESH_MS,
		refetchOnWindowFocus: "always",
		placeholderData: keepPreviousData,
		retry: false,
		meta: { suppressGlobalErrorToast: true },
	});
	const showSpinner = useDelayedFlag(page.isPending);
	const tKinds = useTranslations("notebooks.kinds");

	const openSource = useCallback(
		(source: { id: number; title: string }, pageNumber?: number | null) =>
			setTarget({
				type: "source",
				sourceId: source.id,
				title: source.title,
				label: pageNumber ? `${t("sources")} · ${t("page", { page: pageNumber })}` : t("sources"),
				page: pageNumber,
			}),
		[t]
	);
	const openCitation = useCallback(
		(citation: LiveCitation) =>
			openSource({ id: citation.source_id, title: citation.title }, citation.page),
		[openSource]
	);
	const kindLabel = useCallback(
		(kind: string) => (tKinds.has(`${kind}.label`) ? tKinds(`${kind}.label`) : t("studio")),
		[tKinds, t]
	);
	const openOutput = useCallback(
		(output: { artifact_id: number; title: string; kind?: string }) =>
			setTarget({
				type: "artifact",
				artifactId: output.artifact_id,
				title: output.title,
				label: output.kind ? `${t("studio")} · ${kindLabel(output.kind)}` : t("made_in_chat"),
			}),
		[t, kindLabel]
	);
	const closeViewer = useCallback(() => setTarget(null), []);

	// A link that stopped working while open (expired, revoked, notebook
	// deleted) says so; a passing network error keeps the last read painted.
	const failure =
		page.error instanceof LiveLinkError && page.error.status < 500
			? publicLinkFailure(page.error)
			: null;
	if (failure || (page.isError && !page.data)) {
		return <PublicLinkUnavailable reason={failure ?? "error"} />;
	}
	if (!page.data) {
		return (
			<main className="min-h-screen overflow-x-hidden bg-main-panel text-foreground">
				<PublicHeader />
				<div className="flex h-screen items-center justify-center">
					{showSpinner ? <Spinner size="lg" className="text-muted-foreground" /> : null}
				</div>
			</main>
		);
	}

	const data: LiveNotebook = page.data;
	const tabs: Tab[] = ["sources", "chat", "studio"];
	return (
		<main className="flex h-dvh flex-col overflow-hidden bg-main-panel text-foreground">
			<PublicHeader />
			<div className="flex min-h-0 flex-1 flex-col pt-16">
				<div className="flex shrink-0 items-center gap-3 border-b px-4 py-3">
					<span className="text-2xl leading-none" aria-hidden>
						{data.notebook.icon || "📓"}
					</span>
					<div className="min-w-0 flex-1">
						<h1 className="truncate text-base font-semibold sm:text-lg" data-testid="live-title">
							{data.notebook.title}
						</h1>
						<SharedNotice expiresAt={data.expires_at} />
					</div>
					<span
						className="inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs text-muted-foreground"
						title={t("updates")}
					>
						<span className="relative flex size-2">
							<span className="absolute inline-flex size-full rounded-full bg-emerald-500 opacity-60 motion-safe:animate-ping" />
							<span className="relative inline-flex size-2 rounded-full bg-emerald-500" />
						</span>
						<span>{t("live_badge")}</span>
						<span className="sr-only">{t("updates")}</span>
					</span>
				</div>
				<div role="tablist" className="flex shrink-0 gap-1 border-b px-2 py-1.5 lg:hidden">
					{tabs.map((name) => (
						<button
							key={name}
							type="button"
							role="tab"
							aria-selected={tab === name}
							onClick={() => setTab(name)}
							className={cn(
								"flex-1 rounded-full px-3 py-1.5 text-sm font-medium transition-colors",
								tab === name
									? "bg-primary text-primary-foreground"
									: "text-muted-foreground hover:bg-accent"
							)}
						>
							{name === "sources"
								? `${t("sources")} (${data.sources.length})`
								: name === "studio"
									? `${t("studio")} (${data.outputs.length})`
									: t("chat")}
						</button>
					))}
				</div>
				<div className="flex min-h-0 flex-1">
					<aside
						aria-label={t("sources")}
						className={cn(
							"min-h-0 w-full flex-col overflow-y-auto border-r lg:flex lg:w-72 lg:shrink-0",
							tab === "sources" ? "flex" : "hidden"
						)}
					>
						<h2 className="hidden px-4 pt-4 pb-2 text-sm font-semibold lg:block">{t("sources")}</h2>
						<SourceList sources={data.sources} onOpen={(source) => openSource(source)} />
					</aside>
					<section
						aria-label={t("chat")}
						className={cn(
							"min-h-0 min-w-0 flex-1 flex-col lg:flex",
							tab === "chat" ? "flex" : "hidden"
						)}
					>
						<LiveChat
							messages={data.messages}
							citations={data.citations}
							onOpenSource={openCitation}
							onOpenArtifact={openOutput}
						/>
					</section>
					<aside
						aria-label={t("studio")}
						className={cn(
							"min-h-0 w-full flex-col overflow-y-auto border-l lg:flex lg:w-80 lg:shrink-0",
							tab === "studio" ? "flex" : "hidden"
						)}
					>
						<h2 className="hidden px-4 pt-4 pb-2 text-sm font-semibold lg:block">{t("studio")}</h2>
						<OutputList outputs={data.outputs} kindLabel={kindLabel} onOpen={openOutput} />
					</aside>
				</div>
			</div>
			{target ? <LiveViewer token={token} target={target} onClose={closeViewer} /> : null}
		</main>
	);
}

function SourceList({
	sources,
	onOpen,
}: {
	sources: LiveSource[];
	onOpen: (source: LiveSource) => void;
}) {
	const t = useTranslations("publicSharing.live");
	if (sources.length === 0) {
		return <p className="px-4 py-6 text-sm text-muted-foreground">{t("no_sources")}</p>;
	}
	return (
		<ul className="space-y-0.5 p-2" data-testid="live-sources">
			{sources.map((source) => {
				const web = source.document_type.includes("CRAWL") || source.document_type.includes("URL");
				const Icon = web ? Globe : FileText;
				return (
					<li key={source.id}>
						<button
							type="button"
							onClick={() => onOpen(source)}
							className="flex w-full min-w-0 items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm hover:bg-accent"
							aria-label={t("open", { title: source.title })}
						>
							<Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
							<span className="min-w-0 flex-1 truncate">{source.title}</span>
						</button>
					</li>
				);
			})}
		</ul>
	);
}

function OutputList({
	outputs,
	kindLabel,
	onOpen,
}: {
	outputs: LiveOutput[];
	kindLabel: (kind: string) => string;
	onOpen: (output: LiveOutput) => void;
}) {
	const t = useTranslations("publicSharing.live");
	if (outputs.length === 0) {
		return <p className="px-4 py-6 text-sm text-muted-foreground">{t("no_outputs")}</p>;
	}
	return (
		<ul className="space-y-1 p-2" data-testid="live-outputs">
			{outputs.map((output) => {
				const Icon = STUDIO_TILES.find((tile) => tile.kind === output.kind)?.icon ?? FileText;
				return (
					<li key={output.id}>
						<button
							type="button"
							onClick={() => onOpen(output)}
							className="flex w-full min-w-0 items-center gap-3 rounded-xl border bg-background/60 px-3 py-2.5 text-left hover:bg-accent"
							aria-label={t("open", { title: output.title })}
							data-testid="live-output"
						>
							<span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted">
								<Icon className="size-4 text-muted-foreground" aria-hidden />
							</span>
							<span className="min-w-0 flex-1">
								<span className="block truncate text-sm font-medium">{output.title}</span>
								<span className="block text-xs text-muted-foreground">
									{kindLabel(output.kind)}
									{output.duration_seconds
										? ` · ${Math.floor(output.duration_seconds / 60)}:${String(
												output.duration_seconds % 60
											).padStart(2, "0")}`
										: null}
								</span>
							</span>
						</button>
					</li>
				);
			})}
		</ul>
	);
}
