"use client";

import { useQuery } from "@tanstack/react-query";
import type { MCPOAuthDiscovery } from "@/contracts/types/mcp.types";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";
import { canDiscover } from "../model/server-form-state";

export interface OAuthDiscoveryState {
	/** The answer for the URL as last settled, or undefined while none is in. */
	discovery: MCPOAuthDiscovery | undefined;
	checking: boolean;
}

/**
 * What OAuth sign-in the server at `url` offers, asked once the person has
 * stopped typing (a request per keystroke would be a request per letter to
 * a stranger's server). Asked only while OAuth is the chosen sign-in and the
 * URL is a complete https address.
 */
export function useOAuthDiscovery(
	url: string,
	enabled: boolean,
	connectorId?: number
): OAuthDiscoveryState {
	const settled = useDebouncedValue(url.trim(), 600);
	const ask = enabled && canDiscover(settled);
	const { data, isFetching } = useQuery({
		queryKey: ["mcp-oauth-discovery", settled, connectorId ?? null],
		queryFn: () => connectorsApiService.discoverMCPOAuth(settled, connectorId),
		enabled: ask,
		staleTime: 5 * 60_000,
		retry: false,
		meta: { suppressGlobalErrorToast: true },
	});
	return { discovery: ask ? data : undefined, checking: ask && isFetching };
}
