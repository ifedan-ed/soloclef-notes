"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { type ChatModel, chooseFailureSentence, withChoice } from "./chat-model";
import { chatModelApi } from "./chat-model-api";

/**
 * The account's chat model and the models it may choose.
 *
 * The list is the proxy's own, which the server already remembers for a
 * while, so a short stale time here costs one small request per minute at
 * most and lets a model added to the proxy show up without a reload. The
 * picker and the settings page share the one cache entry.
 */
export function useChatModel() {
	return useQuery<ChatModel, Error>({
		queryKey: cacheKeys.chatModel(),
		queryFn: () => chatModelApi.read(),
		staleTime: 60_000,
		// Whoever shows the model says in words when it cannot be read.
		meta: { suppressGlobalErrorToast: true },
	});
}

/**
 * Choose the account's chat model; null goes back to the server's default.
 *
 * The new choice is shown at once and put back if the server refuses it,
 * with the server's sentence in a toast. A refusal also re-reads the list:
 * the usual cause is a model that left the proxy after the list was read.
 */
export function useChooseChatModel() {
	const queryClient = useQueryClient();
	const key = cacheKeys.chatModel();
	return useMutation<ChatModel, Error, string | null, { previous?: ChatModel }>({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: (model) => chatModelApi.choose(model),
		onMutate: async (model) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<ChatModel>(key);
			if (previous) queryClient.setQueryData(key, withChoice(previous, model));
			return { previous };
		},
		onSuccess: (saved) => queryClient.setQueryData(key, saved),
		onError: (error, _model, context) => {
			if (context?.previous) queryClient.setQueryData(key, context.previous);
			toast.error(chooseFailureSentence(error));
			void queryClient.invalidateQueries({ queryKey: key });
		},
	});
}
