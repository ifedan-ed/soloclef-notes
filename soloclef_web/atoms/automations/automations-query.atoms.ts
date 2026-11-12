import { atomWithQuery } from "jotai-tanstack-query";
import { automationsApiService } from "@/lib/apis/automations-api.service";
import { isAuthenticated } from "@/lib/auth-utils";
import { cacheKeys } from "@/lib/query-client/cache-keys";

// First page of the account's automations.
// Detail + paginated/parameterized reads live in hooks (see use-automation.ts,
// use-automation-runs.ts) so atoms don't proliferate into atom families for
// every (id, limit, offset) tuple.
const DEFAULT_LIMIT = 50;
const DEFAULT_OFFSET = 0;

export const automationsListAtom = atomWithQuery(() => ({
	queryKey: cacheKeys.automations.list(DEFAULT_LIMIT, DEFAULT_OFFSET),
	// The server takes the account from the session, so the only thing to
	// wait for is a session to send.
	enabled: isAuthenticated(),
	staleTime: 60 * 1000,
	queryFn: () =>
		automationsApiService.listAutomations({ limit: DEFAULT_LIMIT, offset: DEFAULT_OFFSET }),
}));
