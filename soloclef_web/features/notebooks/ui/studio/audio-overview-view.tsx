"use client";

import { ArrowLeft, Globe } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { PodcastPlayer } from "@/components/tool-ui/podcast/player";
import { Button } from "@/components/ui/button";
import { publicLinksApi, ShareDialog } from "@/features/public-sharing";
import type { StudioOutput } from "../../model/notebook";

/**
 * An audio overview, played in the Studio column: back, its title, Share
 * publicly, then the player and transcript.
 *
 * Audio plays here rather than in the output view the other kinds open in
 * (output-view.tsx), so the output view's Share never reached it and an audio
 * overview could be shared only from the phone app. This header offers the
 * same Share: the same dialog, the same public link to the Studio output,
 * which opens for a visitor in the shared page's own player.
 */
export function AudioOverviewView({
	notebookId,
	output,
	artifactId,
	onBack,
}: {
	notebookId: number;
	output: StudioOutput;
	/** The audio file's artifact; only a finished overview has one, and only it is shown here. */
	artifactId: number;
	onBack: () => void;
}) {
	const t = useTranslations("notebooks");
	const tShare = useTranslations("publicSharing");
	const [shareOpen, setShareOpen] = useState(false);

	return (
		<div className="flex min-h-0 flex-1 flex-col" data-testid="audio-overview-view">
			<ShareDialog
				open={shareOpen}
				onOpenChange={setShareOpen}
				itemTitle={output.title}
				create={(expiry) => publicLinksApi.shareOutput(notebookId, output.id, expiry)}
			/>
			<div className="flex items-center gap-2 px-3 pb-2">
				<Button
					variant="ghost"
					size="icon"
					className="size-8 shrink-0 rounded-full"
					onClick={onBack}
					aria-label={t("back_to_studio")}
				>
					<ArrowLeft className="size-4" />
				</Button>
				<h3 className="min-w-0 flex-1 truncate text-sm font-medium">{output.title}</h3>
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
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto px-3">
				<PodcastPlayer
					artifactId={artifactId}
					title={output.title}
					durationMs={output.duration_seconds ? output.duration_seconds * 1000 : undefined}
				/>
			</div>
		</div>
	);
}
