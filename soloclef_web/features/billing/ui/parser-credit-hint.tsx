"use client";

import { useLocale, useTranslations } from "next-intl";
import { useBillingMe } from "../hooks/use-billing-queries";
import { formatCredits } from "../lib/format";
import { priceFor } from "../lib/plans";

/** "Cloud: N credits/page" / "Local: free" under a parser choice, from the price table. */
export function ParserCreditHint({ parser }: { parser: string }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const me = useBillingMe().data;
	if (!me || me.mode === "off" || me.exempt) return null;
	const price = priceFor(me.prices, parser === "cloud" ? "cloud_page" : "local_page");
	if (!price) return null;
	return (
		<span className="block" data-testid={`parser-credit-hint-${parser}`}>
			{price.credits > 0
				? t("hints.credits_per_page", {
						count: price.credits,
						credits: formatCredits(price.credits, locale),
					})
				: t("hints.free_parsing")}
		</span>
	);
}
