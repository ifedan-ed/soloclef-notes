import { atomWithMutation } from "jotai-tanstack-query";
import { toast } from "sonner";
import { automationsApiService } from "@/lib/apis/automations-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";

/**
 * Cancel one pending or running run. The run list needs no refetch: its
 * status arrives live over Zero. Only the run's detail (which carries the
 * "Cancelled" reason) is refreshed.
 */
export const cancelRunMutationAtom = atomWithMutation(() => ({
	meta: { suppressGlobalErrorToast: true },
	mutationFn: async (vars: { automationId: number; runId: number }) => {
		return automationsApiService.cancelRun(vars.automationId, vars.runId);
	},
	onSuccess: (_run, vars) => {
		queryClient.invalidateQueries({
			queryKey: cacheKeys.automations.run(vars.automationId, vars.runId),
		});
		toast.success("Run cancelled");
	},
	onError: (error: Error) => {
		// 409 says why ("This run already finished.").
		toast.error(error.message || "Couldn't cancel the run");
	},
}));
