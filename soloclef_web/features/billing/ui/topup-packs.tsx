"use client";

import { useLocale, useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import type { Pack } from "../api/billing-schemas";
import { formatCredits, formatMoney } from "../lib/format";

/**
 * Credit packs: one-off top-ups that never expire with the period. Without
 * `onBuy` (no payment method is set up) they are listed without a Buy button.
 */
export function TopupPacks({ packs, onBuy }: { packs: Pack[]; onBuy?: (pack: Pack) => void }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	if (packs.length === 0) return <p className="text-sm text-muted-foreground">{t("no_packs")}</p>;
	return (
		<ul className="grid gap-3 sm:grid-cols-2">
			{packs.map((pack) => (
				<li
					key={pack.id}
					className="flex min-w-0 items-center justify-between gap-3 rounded-lg border bg-card p-4"
					data-testid={`billing-pack-${pack.slug}`}
				>
					<div className="min-w-0">
						<p className="truncate font-medium">{pack.name}</p>
						<p className="text-sm text-muted-foreground">
							{t("pack_line", {
								credits: formatCredits(pack.credits, locale),
								price: formatMoney(pack.price_cents, pack.currency, locale),
							})}
						</p>
					</div>
					{onBuy ? (
						<Button
							variant="outline"
							onClick={() => onBuy(pack)}
							aria-label={`${t("buy")}: ${pack.name}`}
						>
							{t("buy")}
						</Button>
					) : null}
				</li>
			))}
		</ul>
	);
}
