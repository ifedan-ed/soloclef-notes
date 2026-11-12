"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { chooseFailureSentence } from "@/features/chat-model/chat-model";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import {
	type ModelChoice,
	type ModelJob,
	type ModelRole,
	type ModelRoles,
	withChoice,
} from "./model-choice";
import { modelChoiceApi } from "./model-choice-api";

const CHOOSE_FAILED = "The model could not be changed. Try again in a moment.";

/**
 * The sentence for a refused choice. The server names the field after what
 * was sent (`model`, `vision`, `image`); chat-model's reader looks for
 * `model`, so a role's refusal is looked for under its own name first.
 */
function failureSentence(error: unknown, field: string): string {
	const fields = (error as { fields?: Array<{ loc?: unknown; msg?: unknown }> } | null)?.fields;
	if (Array.isArray(fields)) {
		const own = fields.find((entry) => Array.isArray(entry.loc) && entry.loc.at(-1) === field);
		if (typeof own?.msg === "string" && own.msg) return own.msg;
	}
	const sentence = chooseFailureSentence(error);
	return sentence || CHOOSE_FAILED;
}

/** The model of one of the account's jobs, and the models it may choose. */
export function useModelChoice(job: ModelJob) {
	return useQuery<ModelChoice, Error>({
		queryKey: cacheKeys.modelChoice(job),
		queryFn: () => modelChoiceApi.read(job),
		staleTime: 60_000,
		// The setting says in words when it cannot be read.
		meta: { suppressGlobalErrorToast: true },
	});
}

/**
 * Choose a job's model; null goes back to the default. The choice is shown
 * at once and put back if the server refuses it, with the server's sentence.
 */
export function useChooseModel(job: ModelJob) {
	const queryClient = useQueryClient();
	const key = cacheKeys.modelChoice(job);
	return useMutation<ModelChoice, Error, string | null, { previous?: ModelChoice }>({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: (model) => modelChoiceApi.choose(job, model),
		onMutate: async (model) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<ModelChoice>(key);
			if (previous) queryClient.setQueryData(key, withChoice(previous, model));
			return { previous };
		},
		onSuccess: (saved) => queryClient.setQueryData(key, saved),
		onError: (error, _model, context) => {
			if (context?.previous) queryClient.setQueryData(key, context.previous);
			toast.error(failureSentence(error, "model"));
			void queryClient.invalidateQueries({ queryKey: key });
		},
	});
}

/** The account's vision and image models. */
export function useModelRoles() {
	return useQuery<ModelRoles, Error>({
		queryKey: cacheKeys.modelConnections.roles(),
		queryFn: () => modelChoiceApi.readRoles(),
		staleTime: 60_000,
		meta: { suppressGlobalErrorToast: true },
	});
}

/** Choose the vision or the image model; null goes back to the default. */
export function useChooseModelRole(role: ModelRole) {
	const queryClient = useQueryClient();
	const key = cacheKeys.modelConnections.roles();
	return useMutation<ModelRoles, Error, string | null, { previous?: ModelRoles }>({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: (model) => modelChoiceApi.chooseRole(role, model),
		onMutate: async (model) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<ModelRoles>(key);
			if (previous) {
				queryClient.setQueryData(key, { ...previous, [role]: withChoice(previous[role], model) });
			}
			return { previous };
		},
		onSuccess: (saved) => queryClient.setQueryData(key, saved),
		onError: (error, _model, context) => {
			if (context?.previous) queryClient.setQueryData(key, context.previous);
			toast.error(failureSentence(error, role));
			void queryClient.invalidateQueries({ queryKey: key });
		},
	});
}
