import { atomWithMutation } from "jotai-tanstack-query";
import type {
	CreateLogRequest,
	DeleteLogRequest,
	UpdateLogRequest,
} from "@/contracts/types/log.types";
import { logsApiService } from "@/lib/apis/logs-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";

/**
 * Create Log Mutation
 */
export const createLogMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.logs.list(),
		mutationFn: async (request: CreateLogRequest) => logsApiService.createLog(request),
		onSuccess: () => {
			// Invalidate all log-related queries (list, summary, detail, withQueryParams)
			queryClient.invalidateQueries({ queryKey: ["logs"] });
		},
	};
});

/**
 * Update Log Mutation
 */
export const updateLogMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.logs.list(),
		mutationFn: async ({ logId, data }: { logId: number; data: UpdateLogRequest }) =>
			logsApiService.updateLog(logId, data),
		onSuccess: (_data, variables) => {
			queryClient.invalidateQueries({ queryKey: ["logs"] });
		},
	};
});

/**
 * Delete Log Mutation
 */
export const deleteLogMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.logs.list(),
		mutationFn: async (request: DeleteLogRequest) => logsApiService.deleteLog(request),
		onSuccess: (_data, request) => {
			queryClient.invalidateQueries({ queryKey: ["logs"] });
		},
	};
});
