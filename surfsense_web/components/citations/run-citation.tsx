"use client";

import { useSetAtom } from "jotai";
import { Globe2 } from "lucide-react";
import { useTranslations } from "next-intl";
import type { FC } from "react";
import { useState } from "react";
import { openRunCitationPanelAtom } from "@/atoms/citation/citation-panel.atom";
import { RunCitationPanelContent } from "@/components/citations/run-citation-panel";
import { Button } from "@/components/ui/button";
import {
	Drawer,
	DrawerContent,
	DrawerHandle,
	DrawerHeader,
	DrawerTitle,
} from "@/components/ui/drawer";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useMediaQuery } from "@/hooks/use-media-query";

/**
 * Inline citation for a scraper run (the composer's Tools menu, a web
 * specialist): reads "Web" so it is never mistaken for one of the notebook's
 * sources. Opens the run, with the pages it read, in the citation panel.
 */
export const RunCitation: FC<{ runId: string }> = ({ runId }) => {
	const t = useTranslations("run_citation");
	const openRunPanel = useSetAtom(openRunCitationPanelAtom);
	const isDesktop = useMediaQuery("(min-width: 768px)");
	const [mobilePreviewOpen, setMobilePreviewOpen] = useState(false);

	return (
		<>
			<Tooltip>
				<TooltipTrigger asChild>
					<Button
						type="button"
						variant="ghost"
						onClick={() => (isDesktop ? openRunPanel({ runId }) : setMobilePreviewOpen(true))}
						className="ml-0.5 inline-flex h-5 min-w-5 items-center justify-center gap-0.5 rounded-md bg-popover px-1.5 text-[11px] font-medium text-popover-foreground/80 align-baseline"
						aria-label={t("aria")}
					>
						<Globe2 className="size-3" />
						{t("label")}
					</Button>
				</TooltipTrigger>
				<TooltipContent>{t("tooltip")}</TooltipContent>
			</Tooltip>
			<Drawer
				open={mobilePreviewOpen}
				onOpenChange={setMobilePreviewOpen}
				shouldScaleBackground={false}
			>
				<DrawerContent
					className="h-[85vh] max-h-[85vh] z-80 overflow-hidden"
					overlayClassName="z-80"
				>
					<DrawerHandle />
					<DrawerHeader className="pb-0">
						<DrawerTitle>{t("title")}</DrawerTitle>
					</DrawerHeader>
					<div className="min-h-0 flex-1 flex flex-col overflow-hidden">
						<RunCitationPanelContent runId={runId} />
					</div>
				</DrawerContent>
			</Drawer>
		</>
	);
};
