"use client";

import { useLocale, useTranslations } from "next-intl";
import { Progress } from "@/components/ui/progress";
import type { BillingMe } from "../api/billing-schemas";
import { allowancePercent, formatCredits } from "../lib/format";

/** Credits left this period: the plan allowance with a bar, then top-ups and the total. */
export function CreditsCard({ me }: { me: BillingMe }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const { balances } = me;
	const percent = allowancePercent(balances.plan_credits, balances.plan_allowance);
	const low =
		me.mode === "live" &&
		!me.exempt &&
		me.low_balance_credits != null &&
		balances.spendable_credits <= me.low_balance_credits;
	const n = (value: number) => formatCredits(value, locale);

	return (
		<section
			className="rounded-lg border bg-card p-4 sm:p-5"
			aria-labelledby="billing-credits"
			data-testid="billing-credits"
		>
			<h2
				id="billing-credits"
				className="text-xs font-medium uppercase tracking-wide text-muted-foreground"
			>
				{t("credits_title")}
			</h2>
			{me.exempt ? (
				<p className="mt-2 text-sm">{t("credits_unlimited")}</p>
			) : (
				<>
					<p className="mt-2 text-sm">
						{t("plan_credits_of", {
							left: n(balances.plan_credits),
							allowance: n(balances.plan_allowance),
						})}
					</p>
					<Progress
						value={percent}
						className="mt-2 h-2"
						role="progressbar"
						aria-label={t("plan_credits_label")}
						aria-valuemin={0}
						aria-valuemax={100}
						aria-valuenow={percent}
					/>
				</>
			)}
			<dl className="mt-4 grid grid-cols-2 gap-3 text-sm sm:grid-cols-3">
				<div className="min-w-0">
					<dt className="text-muted-foreground">{t("topup_balance")}</dt>
					<dd className="font-medium tabular-nums">{n(balances.topup_credits)}</dd>
				</div>
				<div className="min-w-0">
					<dt className="text-muted-foreground">{t("spendable")}</dt>
					<dd className="font-medium tabular-nums" data-testid="billing-spendable">
						{n(balances.spendable_credits)}
					</dd>
				</div>
				<div className="min-w-0">
					<dt className="text-muted-foreground">{t("used_this_period")}</dt>
					<dd className="font-medium tabular-nums">{n(balances.period_used_credits)}</dd>
				</div>
			</dl>
			{balances.reserved_credits > 0 ? (
				<p className="mt-2 text-xs text-muted-foreground">
					{t("reserved_note", { credits: n(balances.reserved_credits) })}
				</p>
			) : null}
			{low ? <p className="mt-2 text-sm text-destructive">{t("low_balance")}</p> : null}
		</section>
	);
}
