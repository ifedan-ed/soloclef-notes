import { atomWithMutation } from "jotai-tanstack-query";
import { toast } from "sonner";
import type {
	CreateDocumentRequest,
	DeleteDocumentRequest,
	GetDocumentsResponse,
	UploadDocumentRequest,
} from "@/contracts/types/document.types";
import { documentsApiService } from "@/lib/apis/documents-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";
import { globalDocumentsQueryParamsAtom } from "./ui.atoms";

export const createDocumentMutationAtom = atomWithMutation((get) => {
	const documentsQueryParams = get(globalDocumentsQueryParamsAtom);

	return {
		mutationKey: cacheKeys.documents.globalQueryParams(documentsQueryParams),
		mutationFn: async (request: CreateDocumentRequest) => {
			return documentsApiService.createDocument(request);
		},

		onSuccess: () => {
			toast.success("Document created successfully");
			queryClient.invalidateQueries({
				queryKey: cacheKeys.documents.globalQueryParams(documentsQueryParams),
			});
		},
	};
});

export const uploadDocumentMutationAtom = atomWithMutation((get) => {
	const documentsQueryParams = get(globalDocumentsQueryParamsAtom);

	return {
		mutationKey: cacheKeys.documents.globalQueryParams(documentsQueryParams),
		mutationFn: async (request: UploadDocumentRequest) => {
			return documentsApiService.uploadDocument(request);
		},

		onSuccess: () => {
			// Note: Toast notification is handled by the caller (DocumentUploadTab) to use i18n
			// The prefix of every activity-summary key, so this holds whatever
			// the summary is keyed by after it.
			queryClient.invalidateQueries({ queryKey: ["logs", "summary"] });
		},
	};
});

export const deleteDocumentMutationAtom = atomWithMutation((get) => {
	const documentsQueryParams = get(globalDocumentsQueryParamsAtom);

	return {
		mutationKey: cacheKeys.documents.globalQueryParams(documentsQueryParams),
		mutationFn: async (request: DeleteDocumentRequest) => {
			return documentsApiService.deleteDocument(request);
		},

		onSuccess: (_, request: DeleteDocumentRequest) => {
			// Note: Toast is handled by the caller (page.tsx onBulkDelete) to show count info
			queryClient.setQueryData(
				cacheKeys.documents.globalQueryParams(documentsQueryParams),
				(oldData: GetDocumentsResponse | undefined) => {
					if (!oldData) return oldData;
					return {
						...oldData,
						items: oldData.items.filter((doc) => doc.id !== request.id),
						total: oldData.total - 1,
					};
				}
			);
			queryClient.invalidateQueries({
				queryKey: cacheKeys.documents.document(String(request.id)),
			});
		},
	};
});
