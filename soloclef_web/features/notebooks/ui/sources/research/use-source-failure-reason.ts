"use client";

import { useTranslations } from "next-intl";
import { useCallback } from "react";
import type { ResearchBoxSource } from "../../../model/research-sources";
import { useResearchSourceName } from "./use-research-source-name";

const KNOWN_CODES = new Set([
	"out_of_credit",
	"search_failed",
	"source_blocked",
	"source_busy",
	"source_not_connected",
	"source_key_rejected",
	"source_quota",
	"source_timeout",
]);

/**
 * Why a source did not answer, in the user's language when the reason is a
 * known one; otherwise the server's own words (or nothing).
 */
export function useSourceFailureReason(): (
	source: ResearchBoxSource,
	code: string | null | undefined,
	message: string | null | undefined
) => string | null {
	const t = useTranslations("notebook_research");
	const sourceName = useResearchSourceName();
	return useCallback(
		(source, code, message) =>
			code && KNOWN_CODES.has(code)
				? t(`error_${code}`, { source: sourceName(source) })
				: message || null,
		[t, sourceName]
	);
}
