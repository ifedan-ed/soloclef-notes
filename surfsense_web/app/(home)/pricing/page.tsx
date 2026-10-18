import type { Metadata } from "next";
import { PricingCosts, PricingPacks, PricingPlans } from "@/components/site/pricing-sections";
import { FaqSection, PageHero } from "@/components/site/site-sections";
import { BRAND, brandTitle, OG_IMAGES, siteUrl } from "@/lib/brand";
import { howToPay, paymentsEnabled } from "@/lib/pricing/catalog";
import { loadPublicCatalog } from "@/lib/pricing/load-catalog";

/**
 * Pricing. The plans, packs and per-action costs come from the billing catalog
 * (`lib/pricing`), read per request so an admin price change shows without a
 * rebuild. While billing is not live or no payment method is ready, buy
 * buttons read "Coming soon", and "How will I pay?" names only ready methods.
 */
export const dynamic = "force-dynamic";

const TITLE = brandTitle("Pricing");
const DESCRIPTION = `${BRAND.name} plans: Free, Plus and Pro, with monthly credits for chat, parsing and Studio outputs.`;

export const metadata: Metadata = {
	title: TITLE,
	description: DESCRIPTION,
	alternates: { canonical: siteUrl("/pricing") },
	openGraph: {
		title: TITLE,
		description: DESCRIPTION,
		url: siteUrl("/pricing"),
		siteName: BRAND.name,
		images: OG_IMAGES,
	},
};

export default async function PricingPage() {
	const catalog = await loadPublicCatalog();
	const live = paymentsEnabled(catalog);

	return (
		<>
			<PageHero
				eyebrow="Pricing"
				title="Pay for what you use, in credits"
				lede="Every plan comes with credits each month. Chat messages, cloud parsing and Studio outputs spend them; local parsing and search indexing are free."
			>
				{live ? null : (
					<p
						className="mt-6 inline-flex rounded-md border border-[color:var(--border)] bg-[color:var(--card)] px-3 py-2 text-sm text-[color:var(--muted-foreground)]"
						data-testid="billing-not-live"
					>
						Payments are not switched on yet. These are the plans and prices that will apply.
					</p>
				)}
			</PageHero>

			<PricingPlans catalog={catalog} />
			<PricingPacks catalog={catalog} />
			<PricingCosts catalog={catalog} />

			<FaqSection
				heading="About credits"
				items={[
					{
						question: "What is a credit?",
						answer:
							"A unit of use. Each action has a fixed cost in credits, listed above, so you can see what a report or a video will cost before you make it.",
					},
					{
						question: "What happens when I run out?",
						answer:
							"You cannot start actions that cost credits until your plan renews or you add a top-up. Your notebooks, sources and outputs stay where they are.",
					},
					{
						question: "How will I pay?",
						answer: howToPay(catalog),
					},
				]}
			/>
		</>
	);
}
