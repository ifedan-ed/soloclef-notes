import { IconCheck } from "@tabler/icons-react";
import Link from "next/link";
import {
	formatCredits,
	formatPrice,
	monthsPhrase,
	type PublicCatalog,
	paymentCopy,
	paymentsEnabled,
} from "@/lib/pricing/catalog";

/**
 * The pricing page's plan cards, top-ups and per-action costs, drawn from the
 * billing catalog. While billing is not live, or no payment method is set up,
 * every buy button reads "Coming soon" and is disabled; nothing on the page
 * claims payments work, and only the methods that are ready are named.
 */

const UNIT_LABEL: Record<string, string> = {
	turn: "per message",
	page: "per page",
	image: "per image",
	output: "per output",
	document: "per document",
};

function BuyButton({ live, label, href }: { live: boolean; label: string; href: string }) {
	if (!live) {
		return (
			<span
				className="ss-home-btn ss-home-btn-secondary w-full"
				aria-disabled="true"
				data-testid="buy-coming-soon"
			>
				Coming soon
			</span>
		);
	}
	return (
		<Link href={href} className="ss-home-btn ss-home-btn-primary w-full">
			{label}
		</Link>
	);
}

export function PricingPlans({ catalog }: { catalog: PublicCatalog }) {
	const live = paymentsEnabled(catalog);
	// Crypto-only: plans are prepaid for a few months, not a monthly renewal.
	const prepaid =
		live && paymentCopy(catalog.payments) === "crypto_only"
			? monthsPhrase(catalog.payments.cryptoMonthOptions)
			: "";
	return (
		<section className="ss-home-rule" aria-labelledby="plans-heading">
			<h2 id="plans-heading" className="sr-only">
				Plans
			</h2>
			<div className="ss-home-plans">
				{catalog.plans.map((plan) => {
					const free = plan.priceCents === 0;
					return (
						<div key={plan.slug} className="ss-home-plan" data-testid={`plan-${plan.slug}`}>
							<div className="ss-home-plan-head">
								<div className="flex items-center justify-between gap-3">
									<h3 className="ss-home-h3">{plan.name}</h3>
									{plan.highlighted ? <span className="ss-home-plan-badge">Popular</span> : null}
								</div>
								<p className="mt-4 flex items-baseline gap-1.5">
									<span className="ss-home-price">
										{formatPrice(plan.priceCents, catalog.currency)}
									</span>
									<span className="text-sm text-[color:var(--muted-foreground)]">/ month</span>
								</p>
								<p className="ss-home-plan-note mt-2">
									{formatCredits(plan.monthlyCredits)} credits a month
								</p>
								{prepaid && !free ? (
									<p className="ss-home-plan-note" data-testid={`plan-prepaid-${plan.slug}`}>
										Paid ahead in crypto for {prepaid} months
									</p>
								) : null}
								{plan.tagline ? <p className="ss-home-body mt-3 text-sm">{plan.tagline}</p> : null}
								<div className="mt-6">
									{free ? (
										<Link href="/login" className="ss-home-btn ss-home-btn-secondary w-full">
											Sign in
										</Link>
									) : (
										<BuyButton
											live={live}
											label={`Choose ${plan.name}`}
											href={`/dashboard?billing=plan&plan=${encodeURIComponent(plan.slug)}`}
										/>
									)}
								</div>
							</div>
							<ul className="ss-home-plan-features">
								{plan.features.map((feature) => (
									<li key={feature}>
										<IconCheck aria-hidden="true" className="ss-home-plan-check" />
										<span>{feature}</span>
									</li>
								))}
							</ul>
						</div>
					);
				})}
			</div>
		</section>
	);
}

export function PricingPacks({ catalog }: { catalog: PublicCatalog }) {
	if (catalog.packs.length === 0) return null;
	const live = paymentsEnabled(catalog);
	return (
		<section className="ss-home-rule" aria-labelledby="packs-heading">
			<div className="ss-home-head">
				<p className="ss-home-eyebrow">Top-ups</p>
				<h2 id="packs-heading" className="ss-home-h2 mt-2">
					Need more this month?
				</h2>
				<p className="ss-home-body mt-3 max-w-2xl">
					Add a one-time credit pack on any plan. Pack credits never expire; a plan's monthly
					credits reset each month and do not roll over.
				</p>
			</div>
			<div className="ss-home-grid ss-home-grid-2">
				{catalog.packs.map((pack) => (
					<div
						key={pack.slug}
						className="ss-home-cell flex flex-wrap items-center justify-between gap-4"
						data-testid={`pack-${pack.slug}`}
					>
						<div>
							<h3 className="ss-home-h3">{pack.name}</h3>
							<p className="ss-home-body mt-1 text-sm">
								{formatPrice(pack.priceCents, catalog.currency)} once
							</p>
						</div>
						<div className="w-36">
							<BuyButton
								live={live}
								label="Buy"
								href={`/dashboard?billing=pack&pack=${encodeURIComponent(pack.slug)}`}
							/>
						</div>
					</div>
				))}
			</div>
		</section>
	);
}

export function PricingCosts({ catalog }: { catalog: PublicCatalog }) {
	return (
		<section className="ss-home-rule" aria-labelledby="costs-heading">
			<div className="ss-home-head">
				<p className="ss-home-eyebrow">Credits</p>
				<h2 id="costs-heading" className="ss-home-h2 mt-2">
					What each action costs
				</h2>
				<p className="ss-home-body mt-3 max-w-2xl">
					Credits are set aside when an action starts and charged when it succeeds. A job that fails
					is not charged.
				</p>
			</div>
			<section
				className="ss-home-table-scroll"
				aria-label="Credit costs"
				/* biome-ignore lint/a11y/noNoninteractiveTabindex: a region that scrolls horizontally has to be focusable so a keyboard visitor can reach the columns past the fold. */
				tabIndex={0}
			>
				<table className="ss-home-table ss-home-table-compact" data-testid="credit-costs">
					<caption className="sr-only">Credits per action</caption>
					<thead>
						<tr>
							<th scope="col">Action</th>
							<th scope="col">Credits</th>
						</tr>
					</thead>
					<tbody>
						{catalog.prices.map((price) => (
							<tr key={price.action}>
								<th scope="row">{price.label}</th>
								<td>
									{price.credits === 0
										? "Free"
										: `${formatCredits(price.credits)} ${UNIT_LABEL[price.unit] ?? `per ${price.unit}`}`}
								</td>
							</tr>
						))}
					</tbody>
				</table>
			</section>
		</section>
	);
}
