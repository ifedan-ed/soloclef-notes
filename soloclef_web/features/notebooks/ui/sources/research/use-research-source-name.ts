"use client";

import { useTranslations } from "next-intl";
import { useCallback } from "react";
import { isScraperToolId } from "@/features/scraper-tools/scraper-tools";
import type { ResearchBoxSource } from "../../../model/research-sources";

/** The display name of a research source, in the user's language. */
export function useResearchSourceName(): (source: ResearchBoxSource) => string {
	const tools = useTranslations("scraper_tools");
	const research = useTranslations("notebook_research");
	return useCallback(
		(source: ResearchBoxSource) =>
			isScraperToolId(source) ? tools(source) : research(`source_${source}`),
		[tools, research]
	);
}
