"use client";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import type { EtlCapabilities } from "@/contracts/types/document.types";
import { documentsApiService } from "@/lib/apis/documents-api.service";
import { acceptedExtensions, resolveParserChoice } from "@/lib/etl/parser-choice";
import { cacheKeys } from "@/lib/query-client/cache-keys";

/**
 * The parsers a source can choose among: which are configured here, which
 * files each reads, and the upload limits. The server reads all of it from
 * its settings at start-up, so it cannot change without a restart and is
 * cached hard.
 */
export function useEtlCapabilities() {
	return useQuery<EtlCapabilities, Error>({
		queryKey: cacheKeys.etl.capabilities(),
		queryFn: () => documentsApiService.getEtlCapabilities(),
		staleTime: 60 * 60 * 1000,
		gcTime: 60 * 60 * 1000,
	});
}

/**
 * Extensions a folder sync can carry, which is what the server's default
 * parser reads. Undefined until the server has said so, or when its default
 * parser is not configured: there used to be a built-in table to stand in
 * meanwhile, and it was a guess that could name files the server refuses.
 * A caller waits instead.
 */
export function useDefaultParserExtensions(): string[] | undefined {
	const { data: capabilities } = useEtlCapabilities();
	return useMemo(() => {
		if (!capabilities) return undefined;
		const parser = resolveParserChoice(capabilities, undefined).effectiveParser;
		return parser ? Array.from(acceptedExtensions(capabilities, parser)).sort() : undefined;
	}, [capabilities]);
}
