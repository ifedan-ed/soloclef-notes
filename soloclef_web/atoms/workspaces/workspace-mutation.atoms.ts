import { atomWithMutation } from "jotai-tanstack-query";
import { toast } from "sonner";
import type {
	CreateWorkspaceRequest,
	DeleteWorkspaceRequest,
	UpdateWorkspaceApiAccessRequest,
	UpdateWorkspaceRequest,
} from "@/contracts/types/workspace.types";
import { workspacesApiService } from "@/lib/apis/workspaces-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";
import { keyMentionsWorkspace } from "@/lib/workspace-removal";
import { activeWorkspaceIdAtom } from "./workspace-query.atoms";

export const createWorkspaceMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["create-workspace"],
		mutationFn: async (request: CreateWorkspaceRequest) => {
			return workspacesApiService.createWorkspace(request);
		},

		onSuccess: () => {
			toast.success("Workspace created");
			queryClient.invalidateQueries({
				queryKey: cacheKeys.workspaces.all,
			});
		},
	};
});

export const updateWorkspaceMutationAtom = atomWithMutation((get) => {
	const activeWorkspaceId = get(activeWorkspaceIdAtom);

	return {
		mutationKey: ["update-workspace", activeWorkspaceId],
		enabled: !!activeWorkspaceId,
		mutationFn: async (request: UpdateWorkspaceRequest) => {
			return workspacesApiService.updateWorkspace(request);
		},

		onSuccess: (_, request: UpdateWorkspaceRequest) => {
			toast.success("Workspace updated");
			queryClient.invalidateQueries({
				queryKey: cacheKeys.workspaces.all,
			});
			if (request.id) {
				queryClient.invalidateQueries({
					queryKey: cacheKeys.workspaces.detail(String(request.id)),
				});
			}
		},
	};
});

export const updateWorkspaceApiAccessMutationAtom = atomWithMutation((get) => {
	const activeWorkspaceId = get(activeWorkspaceIdAtom);

	return {
		mutationKey: ["update-workspace-api-access", activeWorkspaceId],
		enabled: !!activeWorkspaceId,
		mutationFn: async (request: UpdateWorkspaceApiAccessRequest) => {
			return workspacesApiService.updateWorkspaceApiAccess(request);
		},

		onSuccess: (_, request: UpdateWorkspaceApiAccessRequest) => {
			toast.success("API access updated successfully");
			queryClient.invalidateQueries({
				queryKey: cacheKeys.workspaces.all,
			});
			queryClient.invalidateQueries({
				queryKey: cacheKeys.workspaces.detail(String(request.id)),
			});
		},
	};
});

export const deleteWorkspaceMutationAtom = atomWithMutation((get) => {
	const activeWorkspaceId = get(activeWorkspaceIdAtom);

	return {
		mutationKey: ["delete-workspace", activeWorkspaceId],
		enabled: !!activeWorkspaceId,
		mutationFn: async (request: DeleteWorkspaceRequest) => {
			return workspacesApiService.deleteWorkspace(request);
		},

		onSuccess: (_, request: DeleteWorkspaceRequest) => {
			toast.success("Workspace deleted");
			// Not the whole "workspaces" prefix: that would refetch the deleted
			// workspace's own detail while its page is still open (a 403 toast).
			// The caller drops the deleted workspace's queries once it has moved on.
			queryClient.invalidateQueries({
				queryKey: cacheKeys.workspaces.all,
				predicate: (query) =>
					!keyMentionsWorkspace(query.queryKey.slice(1, 2), request.id ? [request.id] : []),
			});
		},
	};
});
