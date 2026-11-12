import { atomWithMutation } from "jotai-tanstack-query";
import { toast } from "sonner";
import { automationsApiService } from "@/lib/apis/automations-api.service";
import { queryClient } from "@/lib/query-client/client";

/**
 * Start a run of an automation by hand. The new run appears in the list
 * live over Zero; the paged run history is invalidated for the pages that
 * read it over HTTP.
 */
export const runNowMutationAtom = atomWithMutation(() => ({
	meta: { suppressGlobalErrorToast: true },
	mutationFn: async (vars: { automationId: number }) => {
		return automationsApiService.runNow(vars.automationId);
	},
	onSuccess: (_run, vars) => {
		queryClient.invalidateQueries({ queryKey: ["automations", "runs", vars.automationId] });
		toast.success("Run started");
	},
	onError: (error: Error) => {
		// 409 says why (paused, or a run is already waiting or running).
		toast.error(error.message || "Couldn't start the run");
	},
}));
