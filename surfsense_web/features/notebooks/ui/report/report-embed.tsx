"use client";

import { useQuery } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { PodcastPlayer } from "@/components/tool-ui/podcast/player";
import { Spinner } from "@/components/ui/spinner";
import { artifactManifestQueryOptions } from "@/features/artifacts/api/artifact-queries";
import { ArtifactRenderer } from "@/features/artifacts/rendering/artifact-renderer";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { studioTile } from "../../lib/studio-catalog";
import type { StudioOutput } from "../../model/notebook";

/** Fixed heights, so an output that loads never pushes the report around. */
const HEIGHTS: Partial<Record<StudioOutput["kind"], string>> = {
	quiz: "h-[30rem]",
	flashcards: "h-[30rem]",
	mindmap: "h-[26rem]",
	data_table: "h-[24rem]",
	video: "h-[18rem] sm:h-[24rem]",
};

/** A finished Studio output shown inside the report, in its own viewer. */
export function ReportEmbed({
	output,
	workspaceId,
}: {
	output: StudioOutput;
	workspaceId: number;
}) {
	const t = useTranslations("report_view");
	const artifactId = output.artifact_id ?? 0;
	const manifest = useQuery({
		...artifactManifestQueryOptions(workspaceId, artifactId),
		enabled: output.kind !== "audio" && artifactId > 0,
	});
	const showSpinner = useDelayedFlag(manifest.isPending && output.kind !== "audio");
	const [controls, setControls] = useState<HTMLDivElement | null>(null);

	if (output.kind === "audio") {
		return (
			<PodcastPlayer
				artifactId={artifactId}
				workspaceId={workspaceId}
				title={output.title}
				durationMs={output.duration_seconds ? output.duration_seconds * 1000 : undefined}
			/>
		);
	}
	const height = HEIGHTS[output.kind] ?? "h-[26rem] sm:h-[32rem]";
	return (
		<div className="overflow-hidden rounded-xl border bg-background">
			<div
				ref={setControls}
				className="flex min-h-0 items-center justify-end gap-1 px-2 empty:hidden"
			/>
			<div className={`relative ${height}`} aria-busy={manifest.isPending}>
				{manifest.data ? (
					<ArtifactRenderer
						workspaceId={workspaceId}
						manifest={manifest.data}
						zoomControlsContainer={controls}
					/>
				) : manifest.isError ? (
					<p
						role="alert"
						className="flex h-full items-center justify-center px-4 text-sm text-muted-foreground"
					>
						{t("embed_failed")}
					</p>
				) : showSpinner ? (
					<div className="flex h-full items-center justify-center">
						<Spinner size="lg" />
					</div>
				) : null}
			</div>
		</div>
	);
}

/** An added suggestion's finished output, where the suggestion was. */
export function ReportEmbedCard({
	suggestionId,
	output,
	workspaceId,
}: {
	suggestionId: string;
	output: StudioOutput;
	workspaceId: number;
}) {
	const t = useTranslations("report_view");
	const tn = useTranslations("notebooks");
	const kindLabel = tn(`kinds.${output.kind}.label`);
	const Icon = studioTile(output.kind).icon;
	return (
		<section
			aria-label={t("embed_label", { kind: kindLabel, title: output.title })}
			data-report-suggestion={suggestionId}
			className="not-prose my-6 space-y-3"
		>
			<p className="flex min-w-0 items-center gap-2 text-sm">
				<Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
				<span className="shrink-0 text-muted-foreground">{kindLabel}</span>
				<span className="text-muted-foreground" aria-hidden>
					·
				</span>
				<span className="min-w-0 truncate font-medium">{output.title}</span>
			</p>
			<ReportEmbed output={output} workspaceId={workspaceId} />
		</section>
	);
}
