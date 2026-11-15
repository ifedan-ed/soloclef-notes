import { atomWithMutation } from "jotai-tanstack-query";
import { toast } from "sonner";
import { automationsApiService } from "@/lib/apis/automations-api.service";
import { queryClient } from "@/lib/query-client/client";

/**
 * Cancel one pending or running run. The automation's runs (the list and
 * the run's detail, which carries the "Cancelled" reason) are read again at
 * once, rather than waiting for the change stream to report the cancel.
 */
export const cancelRunMutationAtom = atomWithMutation(() => ({
	meta: { suppressGlobalErrorToast: true },
	mutationFn: async (vars: { automationId: number; runId: number }) => {
		return automationsApiService.cancelRun(vars.automationId, vars.runId);
	},
	onSuccess: (_run, vars) => {
		// The runs list's prefix, which the run's detail shares (cache-keys.ts).
		queryClient.invalidateQueries({ queryKey: ["automations", "runs", vars.automationId] });
		toast.success("Run cancelled");
	},
	onError: (error: Error) => {
		// 409 says why ("This run already finished.").
		toast.error(error.message || "Couldn't cancel the run");
	},
}));
