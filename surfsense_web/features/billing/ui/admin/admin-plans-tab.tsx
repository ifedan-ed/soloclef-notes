"use client";

import { Plus } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { AdminOverview, AdminPlan } from "../../api/billing-admin-schemas";
import { formatCredits, formatMoney } from "../../lib/format";
import { PlanEditorDialog } from "./plan-editor-dialog";

/** Every plan, public or not, with its price, credits and flags. */
export function AdminPlansTab({ overview }: { overview: AdminOverview }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const [editing, setEditing] = useState<AdminPlan | "new" | null>(null);
	return (
		<div className="space-y-3">
			<div className="flex justify-end">
				<Button onClick={() => setEditing("new")}>
					<Plus className="size-4" aria-hidden />
					{t("admin.new_plan")}
				</Button>
			</div>
			<ul className="divide-y rounded-lg border">
				{overview.plans.map((plan) => (
					<li key={plan.id} className="flex flex-wrap items-center justify-between gap-2 p-3">
						<div className="min-w-0">
							<p className="flex flex-wrap items-center gap-2 font-medium">
								{plan.name}
								<span className="font-mono text-xs text-muted-foreground">{plan.slug}</span>
								{plan.is_default ? <Badge variant="secondary">{t("admin.default")}</Badge> : null}
								{!plan.public ? <Badge variant="outline">{t("admin.hidden")}</Badge> : null}
								{!plan.active ? <Badge variant="outline">{t("admin.inactive")}</Badge> : null}
							</p>
							<p className="text-sm text-muted-foreground">
								{plan.price_cents > 0
									? t("price_per_month", {
											price: formatMoney(plan.price_cents, plan.currency, locale),
										})
									: t("price_free")}{" "}
								· {t("monthly_credits", { credits: formatCredits(plan.monthly_credits, locale) })}
							</p>
						</div>
						<Button variant="outline" size="sm" onClick={() => setEditing(plan)}>
							{t("admin.edit")}
						</Button>
					</li>
				))}
			</ul>
			<PlanEditorDialog
				plan={editing}
				choices={overview.choices}
				nextSortOrder={overview.plans.length}
				onClose={() => setEditing(null)}
			/>
		</div>
	);
}
