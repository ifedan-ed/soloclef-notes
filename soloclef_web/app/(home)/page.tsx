import type { Metadata } from "next";
import Link from "next/link";
import { HOME_FAQ } from "@/components/site/site-content";
import {
	AdvantagesSection,
	ClosingSection,
	FaqSection,
	PrivacySection,
	SignInActions,
	SignupNote,
	StudioSection,
} from "@/components/site/site-sections";
import { ArrowRightIcon } from "@/components/ui/icons";
import { BRAND, OG_IMAGES, siteUrl } from "@/lib/brand";
import { formatPrice } from "@/lib/pricing/catalog";
import { loadPublicCatalog } from "@/lib/pricing/load-catalog";

/**
 * The landing page, for signed-out visitors. A signed-in visitor never sees it:
 * `proxy.ts` sends "/" with a session cookie to /dashboard.
 *
 * Rendered per request (the plan prices come from the billing catalog), so a
 * price change shows without a rebuild.
 */
export const dynamic = "force-dynamic";

/** "Soloclef Notes · Notebooks that answer from your sources" */
const TITLE = `${BRAND.name} · ${BRAND.tagline.replace(/\.$/, "")}`;

export const metadata: Metadata = {
	title: TITLE,
	description: BRAND.description,
	alternates: { canonical: siteUrl("/") },
	openGraph: {
		title: TITLE,
		description: BRAND.description,
		url: siteUrl("/"),
		siteName: BRAND.name,
		images: OG_IMAGES,
		type: "website",
	},
};

export default async function HomePage() {
	const catalog = await loadPublicCatalog();
	const paid = catalog.plans.filter((plan) => plan.priceCents > 0);
	const cheapest = paid.length
		? paid.reduce((a, b) => (a.priceCents <= b.priceCents ? a : b))
		: null;

	return (
		<>
			<section className="ss-home-hero ss-home-pad">
				<div className="relative mx-auto max-w-3xl text-center">
					<h1 className="ss-home-display">
						Notebooks that answer <span className="ss-home-accent">from your sources</span>
					</h1>
					<p className="ss-home-lede mx-auto mt-6 max-w-2xl">
						Add documents, papers and web research to a notebook. Ask questions and get answers with
						citations back to the passage. Turn the same sources into reports, mind maps, quizzes,
						slides, and audio or video overviews.
					</p>
					<div className="mt-9 flex justify-center">
						<SignInActions secondary={{ label: "See what it does", href: "/features" }} />
					</div>
					<SignupNote />
				</div>
			</section>

			<AdvantagesSection heading="A research notebook that shows its work" />
			<StudioSection />
			<PrivacySection />

			<section className="ss-home-rule">
				<div className="ss-home-pad flex flex-col gap-6 py-12 md:flex-row md:items-center md:justify-between">
					<div className="max-w-xl">
						<p className="ss-home-eyebrow">Pricing</p>
						<h2 className="ss-home-h2 mt-2">Start free, pay for more credits</h2>
						<p className="ss-home-body mt-3">
							The Free plan includes{" "}
							{catalog.plans.find((p) => p.priceCents === 0)?.monthlyCredits ?? 0} credits a month.
							{cheapest
								? ` Paid plans start at ${formatPrice(cheapest.priceCents, catalog.currency)} a month.`
								: ""}
						</p>
					</div>
					<Link
						className="ss-home-btn ss-home-btn-secondary self-start md:self-auto"
						href="/pricing"
					>
						See plans <ArrowRightIcon aria-hidden="true" className="size-4" />
					</Link>
				</div>
			</section>

			<FaqSection items={HOME_FAQ} heading="Before you start" />
			<ClosingSection />
		</>
	);
}
