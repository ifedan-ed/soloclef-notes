"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { TriangleAlert } from "lucide-react";
import { useTranslations } from "next-intl";
import type { ReactNode } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { baseApiService } from "@/lib/apis/base-api.service";
import {
	ALL_AUTO,
	AUTO,
	chooseFallback,
	choosePrimary,
	fallbackValue,
	NO_FALLBACK,
	primaryValue,
	RECOMMENDED,
	type StudioModelRead,
	type StudioModelSettings,
	settingsOf,
	studioModelSchema,
	USE_DEFAULT,
} from "./studio-outputs-choice";

/**
 * The model behind Studio generations (video scripts, podcast transcripts,
 * reports, decks, study sets, mind maps, infographics, data tables): a
 * workspace default plus optional per-type overrides, each with a fallback
 * tried once when the first model fails. Chat and quick tasks are separate.
 */
const path = (workspaceId: number) => `/api/v1/workspaces/${workspaceId}/studio-model`;
export const studioModelKey = (workspaceId: number) => ["studio-model", workspaceId] as const;

export function StudioOutputsModelSetting<M extends { id: number }>({
	workspaceId,
	models,
	renderOption,
}: {
	workspaceId: number;
	models: M[];
	renderOption: (model: M) => ReactNode;
}) {
	const t = useTranslations("studio_model");
	const queryClient = useQueryClient();
	const setting = useQuery({
		queryKey: studioModelKey(workspaceId),
		queryFn: () => baseApiService.get(path(workspaceId), studioModelSchema),
		enabled: workspaceId > 0,
		staleTime: 5 * 60_000,
	});
	const saved = (read: StudioModelRead) =>
		queryClient.setQueryData(studioModelKey(workspaceId), read);
	const failed = (error: Error) => toast.error(error.message || t("save_failed"));
	const update = useMutation({
		mutationFn: (settings: StudioModelSettings) =>
			baseApiService.put(path(workspaceId), studioModelSchema, { body: settings }),
		onSuccess: saved,
		onError: failed,
	});
	const reset = useMutation({
		mutationFn: () => baseApiService.delete(path(workspaceId), studioModelSchema),
		onSuccess: saved,
		onError: failed,
	});

	const data = setting.data;
	if (!data) {
		return (
			<div className="flex flex-col gap-2">
				<Label>{t("title")}</Label>
				<p className="text-xs text-muted-foreground">{t("description")}</p>
			</div>
		);
	}
	// While a change saves, show it rather than the old setting (no flash back).
	const shown = update.isPending && update.variables ? update.variables : settingsOf(data);
	const modelIds = models.map((model) => model.id);
	const change = (next: StudioModelSettings) => update.mutate(next);
	const recommended = data.recommended_fallback
		? t("recommended_option", {
				primary: data.recommended_primary.name,
				fallback: data.recommended_fallback.name,
			})
		: t("recommended_option_single", { primary: data.recommended_primary.name });
	const allAuto = shown.default.mode === "auto" && Object.keys(shown.overrides).length === 0;
	const unavailable = data.unavailable.map((key) =>
		key === "default" ? t("default_label") : t(`slots.${key}`)
	);

	const fallbackSelect = (slot: string | null, label: string, compact: boolean) => {
		const choice = slot === null ? shown.default : shown.overrides[slot];
		const primary = choice?.mode === "model" ? choice.primary : null;
		return (
			<Select
				value={fallbackValue(shown, slot, modelIds)}
				onValueChange={(value) => change(chooseFallback(shown, slot, value))}
				disabled={!primary}
			>
				<SelectTrigger
					className={compact ? "h-8 w-full min-w-0 text-xs" : "w-full"}
					aria-label={label}
				>
					<SelectValue />
				</SelectTrigger>
				<SelectContent>
					<SelectItem value={NO_FALLBACK}>{t("no_fallback")}</SelectItem>
					{models.filter((model) => model.id !== primary).map(renderOption)}
				</SelectContent>
			</Select>
		);
	};

	return (
		<div className="flex flex-col gap-2">
			<Label>{t("title")}</Label>
			<p className="text-xs text-muted-foreground">{t("description")}</p>
			<div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
				<Select
					value={primaryValue(shown, null, modelIds)}
					onValueChange={(value) => change(choosePrimary(shown, null, value))}
				>
					<SelectTrigger className="w-full" aria-label={t("default_label")}>
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={RECOMMENDED}>{recommended}</SelectItem>
						<SelectItem value={AUTO}>{t("auto_option")}</SelectItem>
						{models.map(renderOption)}
					</SelectContent>
				</Select>
				{shown.default.mode === "model"
					? fallbackSelect(null, t("fallback_for", { type: t("default_label") }), false)
					: null}
			</div>
			{unavailable.length ? (
				<p className="flex items-start gap-1.5 text-xs text-amber-600 dark:text-amber-400">
					<TriangleAlert className="mt-px size-3.5 shrink-0" aria-hidden />
					{t("unavailable_warning", { items: unavailable.join(", ") })}
				</p>
			) : null}
			<div className="flex flex-wrap gap-2">
				<Button
					type="button"
					variant="outline"
					size="sm"
					disabled={allAuto || update.isPending}
					onClick={() => change(ALL_AUTO)}
				>
					{t("all_auto")}
				</Button>
				{data.customized ? (
					<Button
						type="button"
						variant="ghost"
						size="sm"
						disabled={reset.isPending || update.isPending}
						onClick={() => reset.mutate()}
					>
						{t("reset")}
					</Button>
				) : null}
			</div>
			<div className="rounded-lg border p-2">
				<table className="w-full table-fixed border-separate border-spacing-x-2 border-spacing-y-1">
					<caption className="sr-only">{t("per_type")}</caption>
					<colgroup>
						<col className="w-[28%]" />
						<col />
						<col />
					</colgroup>
					<thead>
						<tr className="text-left text-xs font-medium text-muted-foreground">
							<th scope="col" className="font-medium">
								{t("type_column")}
							</th>
							<th scope="col" className="font-medium">
								{t("model_column")}
							</th>
							<th scope="col" className="font-medium">
								{t("fallback_column")}
							</th>
						</tr>
					</thead>
					<tbody>
						{data.slots.map((slot) => {
							const label = t(`slots.${slot}`);
							return (
								<tr key={slot}>
									<th scope="row" className="truncate text-left text-xs font-normal">
										{label}
									</th>
									<td className="min-w-0">
										<Select
											value={primaryValue(shown, slot, modelIds)}
											onValueChange={(value) => change(choosePrimary(shown, slot, value))}
										>
											<SelectTrigger
												className="h-8 w-full min-w-0 text-xs"
												aria-label={t("model_for", { type: label })}
											>
												<SelectValue />
											</SelectTrigger>
											<SelectContent>
												<SelectItem value={USE_DEFAULT}>{t("use_default")}</SelectItem>
												<SelectItem value={AUTO}>{t("auto_option")}</SelectItem>
												{models.map(renderOption)}
											</SelectContent>
										</Select>
									</td>
									<td className="min-w-0">
										{fallbackSelect(slot, t("fallback_for", { type: label }), true)}
									</td>
								</tr>
							);
						})}
					</tbody>
				</table>
			</div>
		</div>
	);
}
