"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import type { ReactNode } from "react";
import { toast } from "sonner";
import { z } from "zod";
import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { baseApiService } from "@/lib/apis/base-api.service";

/**
 * The model behind small helper calls (Studio suggestions, follow-up
 * questions, source guides, chat titles). Stored per workspace; "default"
 * means the fast model the backend picks, "same as chat" opts out.
 */
const quickModelSchema = z.object({
	model_id: z.number().nullable(),
	default_model_id: z.number().nullable(),
	default_model_name: z.string(),
});
type QuickModel = z.infer<typeof quickModelSchema>;

const DEFAULT_VALUE = "default";
const SAME_AS_CHAT = 0;

const path = (workspaceId: number) => `/api/v1/workspaces/${workspaceId}/quick-model`;
const quickModelKey = (workspaceId: number) => ["quick-model", workspaceId] as const;

export function selectValue(setting: number | null | undefined, modelIds: number[]): string {
	if (setting === null || setting === undefined) return DEFAULT_VALUE;
	if (setting === SAME_AS_CHAT) return String(SAME_AS_CHAT);
	return modelIds.includes(setting) ? String(setting) : DEFAULT_VALUE;
}

export function QuickTasksModelSetting<M extends { id: number }>({
	workspaceId,
	models,
	renderOption,
}: {
	workspaceId: number;
	models: M[];
	renderOption: (model: M) => ReactNode;
}) {
	const t = useTranslations("quick_model");
	const queryClient = useQueryClient();
	const setting = useQuery({
		queryKey: quickModelKey(workspaceId),
		queryFn: () => baseApiService.get(path(workspaceId), quickModelSchema),
		enabled: workspaceId > 0,
		staleTime: 5 * 60_000,
	});
	const update = useMutation({
		mutationFn: (modelId: number | null) =>
			baseApiService.put(path(workspaceId), quickModelSchema, { body: { model_id: modelId } }),
		onSuccess: (saved: QuickModel) => queryClient.setQueryData(quickModelKey(workspaceId), saved),
		onError: (error: Error) => toast.error(error.message || t("save_failed")),
	});

	const data = setting.data;
	const defaultLabel = data
		? data.default_model_id !== null
			? t("default_option", { model: data.default_model_name })
			: t("default_unavailable", { model: data.default_model_name })
		: t("default_option", { model: "…" });

	return (
		<div className="flex flex-col gap-2">
			<Label>{t("title")}</Label>
			<p className="text-xs text-muted-foreground">{t("description")}</p>
			<Select
				value={selectValue(
					update.isPending ? update.variables : data?.model_id,
					models.map((model) => model.id)
				)}
				onValueChange={(value) => update.mutate(value === DEFAULT_VALUE ? null : Number(value))}
			>
				<SelectTrigger className="w-full" aria-label={t("title")}>
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value={DEFAULT_VALUE}>{defaultLabel}</SelectItem>
					<SelectItem value={String(SAME_AS_CHAT)}>{t("same_as_chat")}</SelectItem>
					{models.map(renderOption)}
				</SelectContent>
			</Select>
		</div>
	);
}
