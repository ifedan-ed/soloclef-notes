"use client";

import { useLocale, useTranslations } from "next-intl";
import type { BillingMe, Pack, Plan } from "../api/billing-schemas";
import {
	canBuyPacks,
	monthList,
	paymentsAvailable,
	planButton,
	planPaymentCopy,
} from "../lib/plans";
import { CreditsCard } from "./credits-card";
import { CurrentPlanCard } from "./current-plan-card";
import { ModeNotice } from "./mode-notice";
import { PaymentsUnavailable } from "./payments-unavailable";
import { type PlanButtonState, PlanCards } from "./plan-cards";
import { SectionHeading } from "./section-heading";
import { SubscriptionActions } from "./subscription-actions";
import { TopupPacks } from "./topup-packs";

/** The plan, credits, plan choices and top-ups for one person. Data in, choices out. */
export function BillingView({
	me,
	onChoosePlan,
	onBuyPack,
}: {
	me: BillingMe;
	onChoosePlan: (plan: Plan) => void;
	onBuyPack: (pack: Pack) => void;
}) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const canPay = paymentsAvailable(me.payments);
	const stateFor = (plan: Plan): PlanButtonState => planButton(plan, me);
	const copy = planPaymentCopy(me.payments);
	const plansDescription =
		copy === "crypto_only"
			? t("plans_description_crypto", {
					months: monthList(me.payments.crypto.month_options, locale),
				})
			: t("plans_description");

	return (
		<div className="space-y-8">
			<ModeNotice mode={me.mode} />

			<div className="grid gap-3 sm:grid-cols-2">
				<CurrentPlanCard me={me} />
				<CreditsCard me={me} />
			</div>

			<SubscriptionActions me={me} />

			<section className="space-y-3" aria-labelledby="billing-plans-title">
				<div id="billing-plans-title">
					<SectionHeading title={t("plans_title")} description={plansDescription} />
				</div>
				{canPay ? null : <PaymentsUnavailable />}
				<PlanCards
					plans={me.plans}
					currentPlanId={me.plan?.id ?? null}
					stateFor={stateFor}
					onChoose={onChoosePlan}
				/>
			</section>

			<section className="space-y-3" aria-labelledby="billing-packs-title">
				<div id="billing-packs-title">
					<SectionHeading title={t("packs_title")} description={t("packs_description")} />
				</div>
				<TopupPacks packs={me.packs} onBuy={canBuyPacks(me.payments) ? onBuyPack : undefined} />
			</section>
		</div>
	);
}
