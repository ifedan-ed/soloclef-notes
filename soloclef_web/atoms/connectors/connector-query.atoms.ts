import { atomWithQuery } from "jotai-tanstack-query";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";
import { isAuthenticated } from "@/lib/auth-utils";
import { cacheKeys } from "@/lib/query-client/cache-keys";

// Every connection of the signed-in account. One list for the whole app: the
// server answers only the caller's own, each with its `enabled` state.
export const connectorsAtom = atomWithQuery(() => {
	return {
		queryKey: cacheKeys.connectors.all(),
		enabled: isAuthenticated(),
		staleTime: 5 * 60 * 1000, // 5 minutes
		queryFn: async () => {
			return connectorsApiService.getConnectors({});
		},
	};
});
