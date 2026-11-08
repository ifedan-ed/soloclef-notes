import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import { isDeletedAutomationKey } from "../automations/deleted-automations";
import { showErrorToast } from "../error-toast";
import { isRemovedWorkspaceError } from "../workspace-removal";
import { shouldRetryQuery } from "./retry";

export const queryClient = new QueryClient({
	defaultOptions: {
		queries: {
			staleTime: 30_000,
			refetchOnWindowFocus: false,
			retry: shouldRetryQuery,
		},
	},
	queryCache: new QueryCache({
		onError: (error, query) => {
			if (query.meta?.suppressGlobalErrorToast) return;
			// A workspace just deleted or left: its stragglers' 403/404s are expected.
			if (isRemovedWorkspaceError(error, query.queryKey)) return;
			// An automation just deleted here: its page's queries 404 until unmounted.
			if (isDeletedAutomationKey(query.queryKey)) return;
			showErrorToast(error);
		},
	}),
	mutationCache: new MutationCache({
		onError: (error, _variables, _context, mutation) => {
			if (mutation.meta?.suppressGlobalErrorToast) return;
			if (isRemovedWorkspaceError(error, mutation.options.mutationKey)) return;
			showErrorToast(error);
		},
	}),
});
