import { atomWithMutation } from "jotai-tanstack-query";
import { toast } from "sonner";
import type {
	ConnectionCreateRequest,
	ConnectionRead,
	ConnectionTestUpdateRequest,
	ConnectionUpdateRequest,
	EndpointModelPreviewRequest,
	ModelPreviewRead,
	ModelPreviewRequest,
	ModelRead,
	ModelsBulkUpdateRequest,
	ModelTestPreviewRequest,
	ModelUpdateRequest,
	VerifyConnectionResponse,
} from "@/contracts/types/model-connections.types";
import { modelConnectionsApiService } from "@/lib/apis/model-connections-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";

function invalidateModelConnections() {
	queryClient.invalidateQueries({
		queryKey: cacheKeys.modelConnections.all(),
	});
	queryClient.invalidateQueries({
		queryKey: cacheKeys.modelConnections.roles(),
	});
	queryClient.invalidateQueries({
		queryKey: cacheKeys.modelConnections.setupStatus(),
	});
}

// After a mutation that can remove the account's last usable chat model,
// surface immediate feedback. The composer's inline notice covers the state on
// its own; this just makes the consequence obvious at the moment of the action.
async function warnIfChatDisabled() {
	try {
		const status = await queryClient.fetchQuery({
			queryKey: cacheKeys.modelConnections.setupStatus(),
			queryFn: () => modelConnectionsApiService.getLlmSetupStatus(),
		});
		if (status?.status === "needs_setup") {
			toast.warning("Chat is now disabled. Connect a chat model to start chatting again.");
		}
	} catch {
		// Non-fatal: the inline composer notice still reflects the state.
	}
}

function upsertModelConnection(connection: ConnectionRead) {
	queryClient.setQueryData<ConnectionRead[]>(cacheKeys.modelConnections.all(), (current = []) => {
		if (current.some((item) => item.id === connection.id)) {
			return current.map((item) => (item.id === connection.id ? connection : item));
		}
		return [...current, connection];
	});
}

export const createModelConnectionMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["model-connections", "create"],
		mutationFn: (request: ConnectionCreateRequest) =>
			modelConnectionsApiService.createConnection(request),
		onSuccess: (connection: ConnectionRead) => {
			toast.success("Connection created");
			upsertModelConnection(connection);
			invalidateModelConnections();
		},
		onError: (error: Error) => toast.error(error.message || "Failed to create connection"),
	};
});

export const updateModelConnectionMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["model-connections", "update"],
		mutationFn: ({ id, data }: { id: number; data: ConnectionUpdateRequest }) =>
			modelConnectionsApiService.updateConnection(id, data),
		onSuccess: () => {
			toast.success("Connection updated");
			invalidateModelConnections();
		},
		onError: (error: Error) => toast.error(error.message || "Failed to update connection"),
	};
});

export const deleteModelConnectionMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["model-connections", "delete"],
		mutationFn: (id: number) => modelConnectionsApiService.deleteConnection(id),
		onSuccess: async () => {
			toast.success("Connection deleted");
			invalidateModelConnections();
			await warnIfChatDisabled();
		},
		onError: (error: Error) => toast.error(error.message || "Failed to delete connection"),
	};
});

export const verifyModelConnectionMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["model-connections", "verify"],
		mutationFn: (id: number) => modelConnectionsApiService.verifyConnection(id),
		onSuccess: (result: VerifyConnectionResponse) => {
			if (result.ok) {
				toast.success("Connection verified");
			} else {
				toast.warning(result.message || "Couldn't verify this connection.");
			}
			invalidateModelConnections();
		},
		onError: (error: Error) => toast.error(error.message || "Failed to verify connection"),
	};
});

export const discoverConnectionModelsMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["model-connections", "discover"],
		mutationFn: (id: number) => modelConnectionsApiService.discoverModels(id),
		onSuccess: (models: ModelRead[]) => {
			// The answer is every model of the connection after the refresh.
			toast.success(`Model list refreshed (${models.length} models)`);
			invalidateModelConnections();
		},
		onError: (error: Error) => toast.error(error.message || "Could not list models"),
	};
});

export const previewConnectionModelsMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["model-connections", "discover-preview"],
		mutationFn: (request: ModelPreviewRequest | EndpointModelPreviewRequest) =>
			modelConnectionsApiService.previewModels(request),
		onSuccess: (_models: ModelPreviewRead[]) => {},
		// No toast: the connect dialog shows the server's sentence beside the
		// list it was asked for, where it is fixed (model-listing.ts,
		// previewFailureOf), and a toast would say it twice.
	};
});

export const testPreviewModelMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["model-connections", "test-preview"],
		mutationFn: (request: ModelTestPreviewRequest) =>
			modelConnectionsApiService.testPreviewModel(request),
		onSuccess: (result: VerifyConnectionResponse) => {
			if (!result.ok) {
				toast.error(result.message || "Model test failed");
			}
		},
		onError: (error: Error) => toast.error(error.message || "Failed to test model"),
	};
});

// Tests a change to a saved connection before it is saved. The server holds
// the saved key and uses it when the change does not name a new one.
export const testConnectionUpdateMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["model-connections", "test-update"],
		mutationFn: ({ id, data }: { id: number; data: ConnectionTestUpdateRequest }) =>
			modelConnectionsApiService.testConnectionUpdate(id, data),
		onSuccess: (result: VerifyConnectionResponse) => {
			if (!result.ok) {
				toast.error(result.message || "Model test failed");
			}
		},
		onError: (error: Error) => toast.error(error.message || "Failed to test model"),
	};
});

export const updateModelMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["models", "update"],
		mutationFn: ({ id, data }: { id: number; data: ModelUpdateRequest }) =>
			modelConnectionsApiService.updateModel(id, data),
		onSuccess: () => invalidateModelConnections(),
		onError: (error: Error) => toast.error(error.message || "Failed to update model"),
	};
});

export const bulkUpdateModelsMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["models", "bulk-update"],
		mutationFn: ({ connectionId, data }: { connectionId: number; data: ModelsBulkUpdateRequest }) =>
			modelConnectionsApiService.bulkUpdateModels(connectionId, data),
		onSuccess: async () => {
			invalidateModelConnections();
			await warnIfChatDisabled();
		},
		onError: (error: Error) => toast.error(error.message || "Failed to update models"),
	};
});

export const testModelMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: ["models", "test"],
		mutationFn: (id: number) => modelConnectionsApiService.testModel(id),
		onSuccess: (result: VerifyConnectionResponse) => {
			if (result.ok) toast.success("Model test succeeded");
			else toast.error(result.message || "Model test failed");
			invalidateModelConnections();
		},
		onError: (error: Error) => toast.error(error.message || "Failed to test model"),
	};
});
