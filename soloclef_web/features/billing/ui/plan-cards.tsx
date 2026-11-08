"use client";

import { Check } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { Plan } from "../api/billing-schemas";
import { formatCredits, formatMoney } from "../lib/format";

/** One plan as a card: its name, price, monthly credits and features, all from the catalog. */
export function PlanCard({
	plan,
	highlighted = false,
	action,
}: {
	plan: Plan;
	highlighted?: boolean;
	action?: ReactNode;
}) {
	const t = useTranslations("billing");
	const locale = useLocale();
	return (
		<article
			className={cn(
				"flex min-w-0 flex-col rounded-lg border bg-card p-4",
				highlighted && "border-primary ring-1 ring-primary/40"
			)}
			aria-label={plan.name}
			data-testid={`billing-plan-${plan.slug}`}
		>
			<h3 className="text-base font-semibold">{plan.name}</h3>
			{plan.tagline ? <p className="text-sm text-muted-foreground">{plan.tagline}</p> : null}
			<p className="mt-3 text-2xl font-semibold tabular-nums">
				{plan.price_cents > 0
					? formatMoney(plan.price_cents, plan.currency, locale)
					: t("price_free")}
				{plan.price_cents > 0 ? (
					<span className="text-sm font-normal text-muted-foreground"> {t("per_month")}</span>
				) : null}
			</p>
			<p className="text-sm text-muted-foreground">
				{t("monthly_credits", { credits: formatCredits(plan.monthly_credits, locale) })}
			</p>
			{plan.features.length > 0 ? (
				<ul className="mt-3 space-y-1.5 text-sm">
					{plan.features.map((feature) => (
						<li key={feature} className="flex gap-2">
							<Check className="mt-0.5 size-4 shrink-0 text-primary" aria-hidden />
							<span className="min-w-0 break-words">{feature}</span>
						</li>
					))}
				</ul>
			) : null}
			{action ? <div className="mt-auto pt-4">{action}</div> : null}
		</article>
	);
}

export type PlanButtonState = "current" | "upgrade" | "downgrade" | "hidden";

/** The plan grid on the billing page, with Current / Upgrade / Downgrade per plan. */
export function PlanCards({
	plans,
	currentPlanId,
	stateFor,
	onChoose,
	busy = false,
}: {
	plans: Plan[];
	currentPlanId: number | null;
	stateFor: (plan: Plan) => PlanButtonState;
	onChoose: (plan: Plan) => void;
	busy?: boolean;
}) {
	const t = useTranslations("billing");
	return (
		<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
			{plans.map((plan) => {
				const state = stateFor(plan);
				return (
					<PlanCard
						key={plan.id}
						plan={plan}
						highlighted={plan.id === currentPlanId}
						action={
							state === "hidden" ? null : state === "current" ? (
								<Button variant="outline" className="w-full" disabled>
									{t("plan_current")}
								</Button>
							) : (
								<Button
									variant={state === "upgrade" ? "default" : "outline"}
									className="w-full"
									disabled={busy}
									onClick={() => onChoose(plan)}
									aria-label={`${t(`plan_${state}`)}: ${plan.name}`}
								>
									{t(`plan_${state}`)}
								</Button>
							)
						}
					/>
				);
			})}
		</div>
	);
}
