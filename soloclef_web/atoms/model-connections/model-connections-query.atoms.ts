import { atomWithQuery } from "jotai-tanstack-query";
import { modelConnectionsApiService } from "@/lib/apis/model-connections-api.service";
import { isAuthenticated } from "@/lib/auth-utils";
import { cacheKeys } from "@/lib/query-client/cache-keys";

// The signed-in account's own connections.
export const modelConnectionsAtom = atomWithQuery(() => ({
	queryKey: cacheKeys.modelConnections.all(),
	enabled: isAuthenticated(),
	staleTime: 5 * 60 * 1000,
	queryFn: () => modelConnectionsApiService.getConnections(),
}));

// One answer per account (whether it can chat), so a plain atom with one key.
export const llmSetupStatusAtom = atomWithQuery(() => ({
	queryKey: cacheKeys.modelConnections.setupStatus(),
	enabled: isAuthenticated(),
	staleTime: 5 * 60 * 1000,
	// Recovery is event-driven: mutations invalidate this key; external fixes
	// are caught on window focus. No polling, so not-ready tabs cost nothing.
	refetchOnWindowFocus: true,
	queryFn: () => modelConnectionsApiService.getLlmSetupStatus(),
}));
