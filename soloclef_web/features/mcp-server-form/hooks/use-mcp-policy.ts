"use client";

import { useQuery } from "@tanstack/react-query";
import type { MCPPolicy } from "@/contracts/types/mcp.types";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";

/**
 * May this person add a server, under which address rule, and is OAuth
 * sign-in set up on this deployment. Unknown until answered: the form then
 * offers what the server will decide anyway, and a failure is not a toast.
 */
export function useMcpPolicy(): MCPPolicy | undefined {
	const { data } = useQuery({
		queryKey: ["mcp-policy"],
		queryFn: () => connectorsApiService.getMCPPolicy(),
		staleTime: 5 * 60_000,
		meta: { suppressGlobalErrorToast: true },
	});
	return data;
}
