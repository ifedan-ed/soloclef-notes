"use client";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useRuntimeConfig } from "@/components/providers/runtime-config";
import type { EtlCapabilities } from "@/contracts/types/document.types";
import { documentsApiService } from "@/lib/apis/documents-api.service";
import { acceptedExtensions } from "@/lib/etl/parser-choice";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { getSupportedExtensionsSet } from "@/lib/supported-extensions";

/**
 * The per-upload Local/Cloud parser choice: which can run, what each costs
 * and which files each opens. Read from the server's environment at boot,
 * so it cannot change without a restart and is cached hard.
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
 * Extensions an upload that names no parser can carry (folder sync sends
 * none, so the server default parser reads them). Falls back to the static
 * table for the default parser's service (lib/etl/fallback-service.ts) until
 * capabilities arrive.
 */
export function useDefaultParserExtensions(): string[] {
	const { data: capabilities } = useEtlCapabilities();
	const { etlService } = useRuntimeConfig();
	return useMemo(
		() =>
			Array.from(
				capabilities
					? acceptedExtensions(capabilities, capabilities.default_parser, {
							visionRequested: false,
						})
					: getSupportedExtensionsSet(undefined, etlService)
			).sort(),
		[capabilities, etlService]
	);
}
