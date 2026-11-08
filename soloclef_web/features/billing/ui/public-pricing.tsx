"use client";

import Link from "next/link";
import { useLocale, useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { useBillingCatalog } from "../hooks/use-billing-queries";
import { formatCredits, formatMoney } from "../lib/format";
import { PlanCard } from "./plan-cards";

/**
 * The pricing page body: public plans, credit packs and what each action
 * costs, all read from the catalog (the database), never from code.
 */
export function PublicPricing() {
	const t = useTranslations("billing");
	const locale = useLocale();
	const catalog = useBillingCatalog();
	const showSkeleton = useDelayedFlag(catalog.isLoading);
	const data = catalog.data;

	return (
		<div className="mx-auto w-full max-w-5xl space-y-12 px-4 py-12 sm:px-6">
			<header className="mx-auto max-w-2xl space-y-3 text-center">
				<h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
					{t("pricing_page.title")}
				</h1>
				<p className="text-muted-foreground">{t("pricing_page.lede")}</p>
			</header>

			{!data ? (
				catalog.isError ? (
					<p className="text-center text-sm text-muted-foreground">{t("errors.load_failed")}</p>
				) : showSkeleton ? (
					<div className="grid gap-3 sm:grid-cols-3">
						<Skeleton className="h-72 w-full" />
						<Skeleton className="h-72 w-full" />
						<Skeleton className="h-72 w-full" />
					</div>
				) : null
			) : (
				<>
					<section aria-labelledby="pricing-plans" className="space-y-4">
						<h2 id="pricing-plans" className="sr-only">
							{t("plans_title")}
						</h2>
						<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
							{data.plans.map((plan) => (
								<PlanCard
									key={plan.id}
									plan={plan}
									action={
										<Button
											asChild
											className="w-full"
											variant={plan.is_default ? "outline" : "default"}
										>
											<Link href="/register">{t("pricing_page.get_started")}</Link>
										</Button>
									}
								/>
							))}
						</div>
					</section>

					{data.packs.length > 0 ? (
						<section aria-labelledby="pricing-packs" className="space-y-3">
							<h2 id="pricing-packs" className="text-xl font-semibold">
								{t("packs_title")}
							</h2>
							<p className="text-sm text-muted-foreground">{t("packs_description")}</p>
							<ul className="grid gap-3 sm:grid-cols-2">
								{data.packs.map((pack) => (
									<li key={pack.id} className="rounded-lg border bg-card p-4">
										<p className="font-medium">{pack.name}</p>
										<p className="text-sm text-muted-foreground">
											{t("pack_line", {
												credits: formatCredits(pack.credits, locale),
												price: formatMoney(pack.price_cents, pack.currency, locale),
											})}
										</p>
									</li>
								))}
							</ul>
						</section>
					) : null}

					{data.prices.length > 0 ? (
						<section aria-labelledby="pricing-costs" className="space-y-3">
							<h2 id="pricing-costs" className="text-xl font-semibold">
								{t("pricing_page.costs_title")}
							</h2>
							<p className="text-sm text-muted-foreground">{t("pricing_page.costs_description")}</p>
							<ul className="divide-y rounded-lg border">
								{data.prices.map((price) => (
									<li key={price.action} className="flex justify-between gap-3 p-3 text-sm">
										<span className="min-w-0">{price.label}</span>
										<span className="shrink-0 tabular-nums text-muted-foreground">
											{price.credits > 0
												? t("pricing_page.credits_each", {
														count: price.credits,
														credits: formatCredits(price.credits, locale),
													})
												: t("pricing_page.included")}
										</span>
									</li>
								))}
							</ul>
						</section>
					) : null}
				</>
			)}
		</div>
	);
}
