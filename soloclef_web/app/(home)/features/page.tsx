import type { Metadata } from "next";
import Link from "next/link";
import { FEATURE_GROUPS } from "@/components/site/site-content";
import {
	ClosingSection,
	PageHero,
	PrivacySection,
	SignupNote,
} from "@/components/site/site-sections";
import { ArrowRightIcon } from "@/components/ui/icons";
import { BRAND, brandTitle, OG_IMAGES, siteUrl } from "@/lib/brand";

const TITLE = brandTitle("Features");
const DESCRIPTION = `What ${BRAND.name} does: sources and research, cited chat, Studio outputs, notes, search and the phone app.`;

export const metadata: Metadata = {
	title: TITLE,
	description: DESCRIPTION,
	alternates: { canonical: siteUrl("/features") },
	openGraph: {
		title: TITLE,
		description: DESCRIPTION,
		url: siteUrl("/features"),
		siteName: BRAND.name,
		images: OG_IMAGES,
	},
};

export default function FeaturesPage() {
	return (
		<>
			<PageHero
				eyebrow="Features"
				title="Everything a notebook can do"
				lede={`${BRAND.name} is built around notebooks: a set of sources, one chat that answers from them, and a Studio that turns them into reports, study material and media.`}
			>
				<SignupNote />
			</PageHero>

			{FEATURE_GROUPS.map((group) => (
				<section key={group.key} id={group.key} className="ss-home-rule scroll-mt-16">
					<div className="ss-home-head">
						<p className="ss-home-eyebrow">{group.eyebrow}</p>
						<h2 className="ss-home-h2 mt-2">{group.title}</h2>
						<p className="ss-home-body mt-3 max-w-2xl">{group.intro}</p>
					</div>
					<div className="ss-home-grid ss-home-grid-2 ss-home-grid-3">
						{group.points.map((point) => (
							<div key={point.title} className="ss-home-cell">
								<h3 className="ss-home-h3">{point.title}</h3>
								<p className="ss-home-body mt-1.5 text-sm">{point.body}</p>
							</div>
						))}
					</div>
					<div className="ss-home-pad border-t border-[color:var(--border)] py-5">
						<Link className="ss-home-forward" href={group.docs}>
							How it works in the docs <ArrowRightIcon aria-hidden="true" className="size-4" />
						</Link>
					</div>
				</section>
			))}

			<PrivacySection />
			<ClosingSection />
		</>
	);
}
