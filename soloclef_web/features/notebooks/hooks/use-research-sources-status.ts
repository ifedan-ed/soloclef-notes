"use client";

import { useQuery } from "@tanstack/react-query";
import { notebookKeys } from "../api/notebook-query-keys";
import { researchApi } from "../api/research-api";
import {
	isResearchSourceId,
	type PageReader,
	type UnavailableSources,
} from "../model/research-sources";

const NONE: UnavailableSources = {};

/**
 * Sources the research menu should grey out (Exa or Tavily with no key in
 * this account). Until the answer arrives, or if it fails, nothing is greyed:
 * a search then reports the missing key inline.
 */
export function useResearchSourcesStatus(notebookId: number): UnavailableSources {
	const { data } = useQuery({
		queryKey: notebookKeys.researchSources(notebookId),
		queryFn: () => researchApi.sourcesStatus(notebookId),
		enabled: notebookId > 0,
		staleTime: 60_000,
		// A background check: its failure is not worth a toast.
		meta: { suppressGlobalErrorToast: true },
		select: (status) => {
			const unavailable: UnavailableSources = {};
			for (const [source, reason] of Object.entries(status.unavailable)) {
				if (isResearchSourceId(source)) unavailable[source] = reason;
			}
			return unavailable;
		},
	});
	return data ?? NONE;
}

/**
 * What reads a pasted page here: Browserbase when the account has it
 * connected, else the crawler. Shares the status request with the hook above.
 */
export function usePageReader(notebookId: number): PageReader {
	const { data } = useQuery({
		queryKey: notebookKeys.researchSources(notebookId),
		queryFn: () => researchApi.sourcesStatus(notebookId),
		enabled: notebookId > 0,
		staleTime: 60_000,
		meta: { suppressGlobalErrorToast: true },
		select: (status) => status.page_reader,
	});
	return data ?? "crawler";
}
