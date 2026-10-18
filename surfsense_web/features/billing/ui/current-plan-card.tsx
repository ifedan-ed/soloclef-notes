"use client";

import { Infinity as InfinityIcon } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import type { BillingMe } from "../api/billing-schemas";
import { formatDate, formatMoney } from "../lib/format";
import { periodEnd, planSourceKind } from "../lib/plans";

/** The plan the person is on, what it costs, how it is held and when it renews or ends. */
export function CurrentPlanCard({ me }: { me: BillingMe }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const plan = me.plan;
	const source = planSourceKind(me);
	const end = formatDate(periodEnd(me.account), locale);
	const sourceLine = (() => {
		switch (source) {
			case "card":
				if (!end) return t("source.card");
				return me.account.cancel_at_period_end
					? t("source.card_ends", { date: end })
					: t("source.card_renews", { date: end });
			case "crypto":
				return end ? t("source.crypto_until", { date: end }) : t("source.crypto");
			case "admin":
				return end ? t("source.admin_until", { date: end }) : t("source.admin");
			default:
				return t("source.free");
		}
	})();

	return (
		<section
			className="rounded-lg border bg-card p-4 sm:p-5"
			aria-labelledby="billing-current-plan"
			data-testid="billing-current-plan"
		>
			<p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
				{t("current_plan")}
			</p>
			<div className="mt-1 flex flex-wrap items-center gap-2">
				<h2 id="billing-current-plan" className="text-xl font-semibold">
					{plan?.name ?? t("no_plan")}
				</h2>
				{me.exempt ? (
					<Badge variant="secondary" className="gap-1">
						<InfinityIcon className="size-3" aria-hidden />
						{t("exempt_badge")}
					</Badge>
				) : null}
				{me.account.cancel_at_period_end ? (
					<Badge variant="outline">{t("cancels_badge")}</Badge>
				) : null}
			</div>
			{plan ? (
				<p className="mt-1 text-sm">
					{plan.price_cents > 0
						? t("price_per_month", { price: formatMoney(plan.price_cents, plan.currency, locale) })
						: t("price_free")}
				</p>
			) : null}
			<p className="mt-1 text-sm text-muted-foreground">{sourceLine}</p>
		</section>
	);
}
