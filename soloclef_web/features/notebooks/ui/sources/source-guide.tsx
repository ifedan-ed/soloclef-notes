"use client";

import { ArrowLeft, TriangleAlert } from "lucide-react";
import { useTranslations } from "next-intl";
import { MarkdownViewer } from "@/components/markdown-viewer";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import { useSourceGuide } from "../../hooks/use-notebook-queries";
import type { NotebookSource } from "../../model/notebook";

export function SourceGuideView({
	workspaceId,
	notebookId,
	source,
	onBack,
}: {
	workspaceId: number;
	notebookId: number;
	source: NotebookSource;
	onBack: () => void;
}) {
	const t = useTranslations("notebooks");
	const guide = useSourceGuide(workspaceId, notebookId, source.id);
	const showSkeleton = useDelayedFlag(guide.isLoading);

	return (
		<div className="flex min-h-0 flex-1 flex-col">
			<div className="flex items-center gap-2 px-3 pb-2">
				<Button
					variant="ghost"
					size="icon"
					className="size-8 rounded-full"
					onClick={onBack}
					aria-label={t("back_to_sources")}
				>
					<ArrowLeft className="size-4" />
				</Button>
				<h3 className="min-w-0 flex-1 truncate text-sm font-medium" title={source.title}>
					{source.title}
				</h3>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
				<p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
					{t("source_guide")}
				</p>
				{guide.isLoading ? (
					showSkeleton ? (
						<div className="space-y-2" aria-busy>
							<Skeleton className="h-3 w-full" />
							<Skeleton className="h-3 w-11/12" />
							<Skeleton className="h-3 w-4/5" />
							<p className="pt-2 text-xs text-muted-foreground">{t("source_guide_generating")}</p>
						</div>
					) : null
				) : guide.data?.summary ? (
					<MarkdownViewer content={guide.data.summary} className="text-sm" />
				) : (
					<p className="flex items-start gap-2 text-sm text-muted-foreground">
						<TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden />
						{t("source_guide_unavailable")}
					</p>
				)}
			</div>
		</div>
	);
}
