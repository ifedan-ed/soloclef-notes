"use client";

import { XIcon } from "lucide-react";
import { useParams } from "next/navigation";
import { useTranslations } from "next-intl";
import type { FC } from "react";
import { Button } from "@/components/ui/button";
import { RunDetail } from "@/features/api-runs/run-detail";

/** Right-panel viewer for a cited scraper run. `runId` is the `run_<uuid>` handle. */
export const RunCitationPanelContent: FC<{
	runId: string;
	onClose?: () => void;
}> = ({ runId, onClose }) => {
	const t = useTranslations("run_citation");
	const params = useParams();
	const rawWorkspaceId = Array.isArray(params?.workspace_id)
		? params.workspace_id[0]
		: params?.workspace_id;
	const workspaceId = Number(rawWorkspaceId);
	const scraperRunId = runId.replace(/^run_/, "");

	return (
		<>
			<div className="shrink-0 flex h-12 items-center justify-between px-3">
				<h2 className="select-none text-lg font-semibold">{t("title")}</h2>
				{onClose && (
					<Button
						variant="ghost"
						size="icon"
						onClick={onClose}
						className="h-8 w-8 rounded-full shrink-0 text-muted-foreground hover:text-accent-foreground"
					>
						<XIcon className="h-4 w-4" />
						<span className="sr-only">{t("close")}</span>
					</Button>
				)}
			</div>

			<div className="flex-1 overflow-y-auto">
				{Number.isFinite(workspaceId) ? (
					<RunDetail workspaceId={workspaceId} runId={scraperRunId} showTopBorder={false} />
				) : (
					<p className="p-4 text-sm text-muted-foreground">{t("no_workspace")}</p>
				)}
			</div>
		</>
	);
};
