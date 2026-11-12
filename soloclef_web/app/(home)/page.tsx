import type { Metadata } from "next";
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
import { BRAND, OG_IMAGES, siteUrl } from "@/lib/brand";

/**
 * The landing page, for signed-out visitors. A signed-in visitor never sees it:
 * `proxy.ts` sends "/" with a session cookie to /dashboard.
 */

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

export default function HomePage() {
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

			<FaqSection items={HOME_FAQ} heading="Before you start" />
			<ClosingSection />
		</>
	);
}
