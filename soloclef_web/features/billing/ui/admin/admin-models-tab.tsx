"use client";

import { useTranslations } from "next-intl";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { billingAdminApi } from "../../api/billing-admin-api";
import { modelTiers } from "../../api/billing-admin-schemas";
import { useAdminModels, useAdminMutation } from "../../hooks/use-billing-admin";

/** Every chat model and whether it bills as standard or premium. */
export function AdminModelsTab() {
	const t = useTranslations("billing");
	const models = useAdminModels();
	const setTier = useAdminMutation((args: { modelId: string; tier: string }) =>
		billingAdminApi.setModelTier(args.modelId, args.tier)
	);
	const showSkeleton = useDelayedFlag(models.isLoading);
	if (models.isError) return <p className="text-sm text-destructive">{t("errors.load_failed")}</p>;
	if (!models.data) return showSkeleton ? <Skeleton className="h-40 w-full" /> : null;
	if (models.data.items.length === 0) {
		return <p className="text-sm text-muted-foreground">{t("admin.no_models")}</p>;
	}
	return (
		<div className="space-y-2">
			<p className="text-sm text-muted-foreground">{t("admin.models_description")}</p>
			<ul className="divide-y rounded-lg border">
				{models.data.items.map((model) => (
					<li
						key={model.model_id}
						className="flex flex-wrap items-center justify-between gap-2 p-3"
					>
						<div className="min-w-0">
							<p className="break-words font-medium">{model.display_name}</p>
							<p className="break-all font-mono text-xs text-muted-foreground">
								{model.model_id} ·{" "}
								{t.has(`admin.model_source.${model.source}`)
									? t(`admin.model_source.${model.source}`)
									: model.source}
							</p>
						</div>
						<SegmentedControl
							ariaLabel={`${t("admin.tier")}: ${model.display_name}`}
							value={model.tier}
							onValueChange={(tier) => setTier.mutate({ modelId: model.model_id, tier })}
							options={modelTiers.map((tier) => ({ value: tier, label: t(`admin.tiers.${tier}`) }))}
						/>
					</li>
				))}
			</ul>
		</div>
	);
}
