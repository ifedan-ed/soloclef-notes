"use client";

import { useTranslations } from "next-intl";
import { useCallback } from "react";
import type { ResearchBoxSource } from "../../../model/research-sources";

/** The display name of a research source, in the user's language. */
export function useResearchSourceName(): (source: ResearchBoxSource) => string {
	const research = useTranslations("notebook_research");
	return useCallback((source: ResearchBoxSource) => research(`source_${source}`), [research]);
}
