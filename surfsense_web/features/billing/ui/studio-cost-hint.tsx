"use client";

import Link from "next/link";
import { useLocale, useTranslations } from "next-intl";
import { useBillingMe } from "../hooks/use-billing-queries";
import { billingSettingsPath } from "../lib/billing-errors";
import { formatCredits } from "../lib/format";
import { planAllowsStudioKind, priceFor } from "../lib/plans";

/**
 * "Uses N credits" for a Studio output, and a note with a link to billing when
 * billing is enforced and the plan does not include this kind. Hidden while
 * billing is off, and quietly absent when billing cannot be read.
 */
export function StudioCostHint({
	kind,
	kindLabel,
	workspaceId,
}: {
	kind: string;
	kindLabel: string;
	workspaceId: number;
}) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const me = useBillingMe().data;
	if (!me || me.mode === "off") return null;
	const price = priceFor(me.prices, `studio_${kind}`);
	const excluded = me.mode === "live" && !me.exempt && !planAllowsStudioKind(me.plan, kind);
	return (
		<div className="space-y-1 text-xs" data-testid="studio-cost-hint">
			{price && !me.exempt ? (
				<p className="text-muted-foreground">
					{t("hints.uses_credits", {
						count: price.credits,
						credits: formatCredits(price.credits, locale),
					})}
				</p>
			) : null}
			{excluded ? (
				<p className="rounded-md border border-destructive/30 bg-destructive/10 px-2 py-1.5 text-destructive">
					{t("hints.plan_excludes", { output: kindLabel })}{" "}
					<Link className="font-medium underline" href={billingSettingsPath(workspaceId)}>
						{t("hints.see_plans")}
					</Link>
				</p>
			) : null}
		</div>
	);
}
