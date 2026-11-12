import { atomWithMutation } from "jotai-tanstack-query";
import type {
	CreateConnectorRequest,
	DeleteConnectorRequest,
	GetConnectorsResponse,
	IndexConnectorRequest,
	IndexConnectorResponse,
	SearchSourceConnector,
	SetConnectorEnabledRequest,
	UpdateConnectorRequest,
} from "@/contracts/types/connector.types";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";

export const createConnectorMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.connectors.all(),
		mutationFn: async (request: CreateConnectorRequest) => {
			return connectorsApiService.createConnector(request);
		},

		onSuccess: () => {
			queryClient.invalidateQueries({
				queryKey: cacheKeys.connectors.all(),
			});
		},
	};
});

export const updateConnectorMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.connectors.all(),
		mutationFn: async (request: UpdateConnectorRequest) => {
			return connectorsApiService.updateConnector(request);
		},

		onSuccess: (_, request: UpdateConnectorRequest) => {
			queryClient.invalidateQueries({
				queryKey: cacheKeys.connectors.all(),
			});
			queryClient.invalidateQueries({
				queryKey: cacheKeys.connectors.byId(String(request.id)),
			});
		},
	};
});

export const deleteConnectorMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.connectors.all(),
		mutationFn: async (request: DeleteConnectorRequest) => {
			return connectorsApiService.deleteConnector(request);
		},

		onSuccess: (_, request: DeleteConnectorRequest) => {
			queryClient.setQueryData(
				cacheKeys.connectors.all(),
				(oldData: GetConnectorsResponse | undefined) => {
					if (!oldData) return oldData;
					return oldData.filter((connector) => connector.id !== request.id);
				}
			);
			queryClient.invalidateQueries({
				queryKey: cacheKeys.connectors.byId(String(request.id)),
			});
		},
	};
});

export const setConnectorEnabledMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.connectors.all(),
		mutationFn: async (request: SetConnectorEnabledRequest) => {
			return connectorsApiService.setConnectorEnabled(request);
		},

		// The answer is the connection as it now stands, so it is written
		// straight into the cached list and its own entry: no refetch.
		onSuccess: (updated: SearchSourceConnector) => {
			queryClient.setQueryData(
				cacheKeys.connectors.all(),
				(oldData: GetConnectorsResponse | undefined) =>
					oldData?.map((connector) => (connector.id === updated.id ? updated : connector))
			);
			queryClient.setQueryData(cacheKeys.connectors.byId(String(updated.id)), updated);
		},
	};
});

export const indexConnectorMutationAtom = atomWithMutation(() => {
	return {
		mutationKey: cacheKeys.connectors.index(),
		mutationFn: async (request: IndexConnectorRequest) => {
			return connectorsApiService.indexConnector(request);
		},

		onSuccess: (response: IndexConnectorResponse) => {
			queryClient.invalidateQueries({
				queryKey: cacheKeys.connectors.all(),
			});
			queryClient.invalidateQueries({
				queryKey: cacheKeys.connectors.byId(String(response.connector_id)),
			});
		},
	};
});
