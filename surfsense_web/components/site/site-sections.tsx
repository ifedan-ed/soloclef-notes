import {
	IconBooks,
	IconBrandNextcloud,
	IconDeviceMobile,
	IconFileSearch,
	IconMessageCircleCheck,
	IconSparkles,
} from "@tabler/icons-react";
import Link from "next/link";
import type { ComponentType } from "react";
import {
	ADVANTAGES,
	type Faq,
	PRIVACY_POINTS,
	REQUEST_ACCESS_HREF,
	SIGNUPS_OPEN,
	STUDIO_OUTPUTS,
} from "@/components/site/site-content";
import { ArrowRightIcon } from "@/components/ui/icons";
import { BRAND } from "@/lib/brand";

/**
 * Sections shared by the landing and features pages. Server components; the
 * FAQ uses native details/summary, so nothing here needs client JavaScript.
 */

const ADVANTAGE_ICONS: Record<string, ComponentType<{ className?: string }>> = {
	grounded: IconMessageCircleCheck,
	studio: IconSparkles,
	research: IconFileSearch,
	parsing: IconBooks,
	nextcloud: IconBrandNextcloud,
	phone: IconDeviceMobile,
};

export function SignInActions({ secondary }: { secondary?: { label: string; href: string } }) {
	return (
		<div className="flex flex-wrap items-center gap-3">
			<Link href="/login" className="ss-home-btn ss-home-btn-primary">
				Sign in
			</Link>
			{secondary ? (
				<Link href={secondary.href} className="ss-home-btn ss-home-btn-secondary">
					{secondary.label}
				</Link>
			) : null}
		</div>
	);
}

export function SignupNote() {
	if (SIGNUPS_OPEN) return null;
	return (
		<p className="mt-4 text-sm text-[color:var(--muted-foreground)]" data-testid="signup-note">
			New accounts are not open yet.{" "}
			<Link className="ss-home-link" href={REQUEST_ACCESS_HREF}>
				Request access
			</Link>
		</p>
	);
}

export function PageHero({
	eyebrow,
	title,
	lede,
	children,
}: {
	eyebrow?: string;
	title: React.ReactNode;
	lede?: React.ReactNode;
	children?: React.ReactNode;
}) {
	return (
		<section className="ss-home-pad py-14 md:py-20">
			<div className="max-w-3xl">
				{eyebrow ? <p className="ss-home-eyebrow">{eyebrow}</p> : null}
				<h1 className="ss-home-display mt-2">{title}</h1>
				{lede ? <p className="ss-home-lede mt-6 max-w-2xl">{lede}</p> : null}
				{children}
			</div>
		</section>
	);
}

export function AdvantagesSection({ heading }: { heading: string }) {
	return (
		<section className="ss-home-rule">
			<div className="ss-home-head">
				<p className="ss-home-eyebrow">Why use it</p>
				<h2 className="ss-home-h2 mt-2">{heading}</h2>
			</div>
			<div className="ss-home-grid ss-home-grid-2 ss-home-grid-3">
				{ADVANTAGES.map((item) => {
					const Icon = ADVANTAGE_ICONS[item.key] ?? IconSparkles;
					return (
						<div key={item.key} className="ss-home-cell flex flex-col" data-testid="advantage">
							<span className="ss-home-icon">
								<Icon aria-hidden="true" className="size-5" />
							</span>
							<h3 className="ss-home-h3 mt-4">{item.title}</h3>
							<p className="ss-home-body mt-2 text-sm">{item.body}</p>
							<p className="mt-auto pt-5">
								<Link className="ss-home-forward" href={item.href}>
									Read more <ArrowRightIcon aria-hidden="true" className="size-4" />
								</Link>
							</p>
						</div>
					);
				})}
			</div>
		</section>
	);
}

export function StudioSection() {
	return (
		<section className="ss-home-rule">
			<div className="ss-home-head">
				<p className="ss-home-eyebrow">Studio</p>
				<h2 className="ss-home-h2 mt-2">Nine things one notebook can become</h2>
			</div>
			<div className="ss-home-grid ss-home-grid-2 ss-home-grid-3">
				{STUDIO_OUTPUTS.map((output) => (
					<div key={output.key} className="ss-home-cell">
						<h3 className="ss-home-h3">{output.name}</h3>
						<p className="ss-home-body mt-1.5 text-sm">{output.body}</p>
					</div>
				))}
			</div>
		</section>
	);
}

export function PrivacySection() {
	return (
		<section className="ss-home-rule">
			<div className="ss-home-head">
				<p className="ss-home-eyebrow">Privacy</p>
				<h2 className="ss-home-h2 mt-2">Where your documents live</h2>
			</div>
			<div className="ss-home-grid ss-home-grid-3">
				{PRIVACY_POINTS.map((point) => (
					<div key={point.title} className="ss-home-cell">
						<h3 className="ss-home-h3">{point.title}</h3>
						<p className="ss-home-body mt-1.5 text-sm">{point.body}</p>
					</div>
				))}
			</div>
			<div className="ss-home-pad border-t border-[color:var(--border)] py-5">
				<Link className="ss-home-forward" href="/privacy">
					The privacy policy in full <ArrowRightIcon aria-hidden="true" className="size-4" />
				</Link>
			</div>
		</section>
	);
}

export function FaqSection({ items, heading }: { items: Faq[]; heading: string }) {
	return (
		<section className="ss-home-rule">
			<div className="ss-home-head">
				<p className="ss-home-eyebrow">Questions</p>
				<h2 className="ss-home-h2 mt-2">{heading}</h2>
			</div>
			<div>
				{items.map((item, index) => (
					<details
						key={item.question}
						className={`ss-home-faq ${index > 0 ? "border-t border-[color:var(--border)]" : ""}`}
					>
						<summary className="ss-home-faq-summary">
							<span>{item.question}</span>
							<span className="ss-home-faq-marker" aria-hidden="true" />
						</summary>
						<p className="ss-home-faq-answer ss-home-body">{item.answer}</p>
					</details>
				))}
			</div>
		</section>
	);
}

export function ClosingSection() {
	return (
		<section className="ss-home-rule">
			<div className="ss-home-pad py-14 md:py-20">
				<h2 className="ss-home-h2 max-w-2xl">Open a notebook and ask it something.</h2>
				<p className="ss-home-body mt-4 max-w-2xl">
					{BRAND.name} works in the browser and on your phone. The docs walk through your first
					notebook in a few minutes.
				</p>
				<div className="mt-8">
					<SignInActions secondary={{ label: "Read the docs", href: "/docs/getting-started" }} />
				</div>
			</div>
		</section>
	);
}
