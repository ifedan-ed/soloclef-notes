"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import {
	type AgentTool,
	type AskFirstChoice,
	agentPermissionsApiService,
} from "@/lib/apis/agent-permissions-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";
import { withChoice } from "../lib/tool-groups";

export function useAgentTools() {
	return useQuery({
		queryKey: cacheKeys.agentPermissions(),
		queryFn: () => agentPermissionsApiService.list(),
		staleTime: 60_000,
	});
}

/**
 * Save one switch at once: the page shows it before the server answers, and
 * a refusal puts that one tool back, with the server's reason in a toast.
 * Only the one tool is put back, so another switch flipped meanwhile keeps
 * its own state.
 */
export function useSetAskFirst() {
	const key = cacheKeys.agentPermissions();
	return useMutation({
		mutationFn: (choice: AskFirstChoice) => agentPermissionsApiService.setAskFirst(choice),
		onMutate: async (choice) => {
			// A read still on its way would land over the change shown here.
			await queryClient.cancelQueries({ queryKey: key });
			queryClient.setQueryData<AgentTool[]>(key, (tools) => tools && withChoice(tools, choice));
		},
		onError: (error, choice) => {
			queryClient.setQueryData<AgentTool[]>(
				key,
				(tools) => tools && withChoice(tools, { ...choice, ask_first: !choice.ask_first })
			);
			toast.error(error instanceof Error && error.message ? error.message : "Could not save this.");
		},
	});
}
