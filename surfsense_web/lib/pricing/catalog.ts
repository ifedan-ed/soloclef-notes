/**
 * The public price list: plans, credit top-ups and what each action costs.
 *
 * The billing system keeps the live catalog in its database (seeded from
 * `surfsense_backend/app/billing/defaults.py`, then admin-edited). Until it
 * exposes a public read of that catalog, the pricing page and the docs use
 * `DEFAULT_CATALOG` below, which mirrors those seeded defaults.
 *
 * To switch to the live catalog, the backend only has to answer
 * `GET /api/v1/billing/public/catalog` with a `PublicCatalog`-shaped body
 * (credits as plain numbers, prices in cents). `loadPublicCatalog` in
 * `load-catalog.ts` tries that route first and falls back to this file.
 *
 * `mode` mirrors the billing setting of the same name: "off", "shadow" or
 * "live". `payments` says which methods can take money right now (switched on
 * and fully set up in the billing admin). Buy buttons are live only when the
 * mode is "live" and at least one method is ready; otherwise they read
 * "Coming soon", and the page names only the methods that are ready.
 */

export type BillingMode = "off" | "shadow" | "live";

export interface PublicPlan {
	slug: string;
	name: string;
	tagline: string;
	priceCents: number;
	monthlyCredits: number;
	features: string[];
	highlighted?: boolean;
}

export interface PublicPack {
	slug: string;
	name: string;
	credits: number;
	priceCents: number;
}

export interface PublicActionPrice {
	action: string;
	label: string;
	credits: number;
	unit: string;
}

export interface PublicPayments {
	card: boolean;
	crypto: boolean;
	/** Prepaid lengths offered for plans by crypto, e.g. [1, 3, 12]. */
	cryptoMonthOptions: number[];
}

export interface PublicCatalog {
	mode: BillingMode;
	currency: string;
	payments: PublicPayments;
	plans: PublicPlan[];
	packs: PublicPack[];
	prices: PublicActionPrice[];
}

export const BILLING_MODES: readonly BillingMode[] = ["off", "shadow", "live"];

export const NO_PAYMENTS: PublicPayments = { card: false, crypto: false, cryptoMonthOptions: [] };

export const DEFAULT_CATALOG: PublicCatalog = {
	mode: "shadow",
	currency: "USD",
	payments: NO_PAYMENTS,
	plans: [
		{
			slug: "free",
			name: "Free",
			tagline: "Try notebooks with your own documents.",
			priceCents: 0,
			monthlyCredits: 100,
			features: [
				"100 credits every month",
				"Local document parsing",
				"Standard models",
				"Every Studio output except video",
			],
		},
		{
			slug: "plus",
			name: "Plus",
			tagline: "For regular study and research.",
			priceCents: 900,
			monthlyCredits: 1500,
			highlighted: true,
			features: [
				"1,500 credits every month",
				"Cloud parsing for scanned and complex files",
				"Image understanding",
				"Premium models",
				"All Studio outputs, videos up to 5 minutes",
			],
		},
		{
			slug: "pro",
			name: "Pro",
			tagline: "For heavy use and long videos.",
			priceCents: 2400,
			monthlyCredits: 5000,
			features: [
				"5,000 credits every month",
				"Everything in Plus",
				"Videos up to 10 minutes",
				"Larger uploads and more jobs at once",
			],
		},
	],
	packs: [
		{ slug: "credits-500", name: "500 credits", credits: 500, priceCents: 500 },
		{ slug: "credits-1200", name: "1,200 credits", credits: 1200, priceCents: 1000 },
	],
	prices: [
		{ action: "chat_standard", label: "Chat message (standard model)", credits: 1, unit: "turn" },
		{ action: "chat_premium", label: "Chat message (premium model)", credits: 5, unit: "turn" },
		{ action: "local_page", label: "Local parsing", credits: 0, unit: "page" },
		{ action: "cloud_page", label: "Cloud parsing", credits: 2, unit: "page" },
		{ action: "vision_image", label: "Image understanding (vision)", credits: 1, unit: "image" },
		{ action: "image_generation", label: "Image generation", credits: 4, unit: "image" },
		{ action: "studio_quiz", label: "Quiz", credits: 5, unit: "output" },
		{ action: "studio_flashcards", label: "Flashcards", credits: 5, unit: "output" },
		{ action: "studio_mindmap", label: "Mind map", credits: 10, unit: "output" },
		{ action: "studio_slides", label: "Slides", credits: 10, unit: "output" },
		{ action: "studio_data_table", label: "Data table", credits: 10, unit: "output" },
		{ action: "studio_infographic", label: "Infographic", credits: 15, unit: "output" },
		{ action: "studio_audio", label: "Audio overview", credits: 15, unit: "output" },
		{ action: "studio_report", label: "Report", credits: 20, unit: "output" },
		{ action: "studio_video", label: "Video overview", credits: 40, unit: "output" },
		{ action: "embedding", label: "Embeddings (search index)", credits: 0, unit: "document" },
	],
};

export function parseBillingMode(value: unknown, fallback: BillingMode = "shadow"): BillingMode {
	return typeof value === "string" && (BILLING_MODES as readonly string[]).includes(value)
		? (value as BillingMode)
		: fallback;
}

/** Buying is offered only when billing is live and some method can take money. */
export function paymentsEnabled(catalog: Pick<PublicCatalog, "mode" | "payments">): boolean {
	return catalog.mode === "live" && (catalog.payments.card || catalog.payments.crypto);
}

/**
 * How the site describes paying: only methods that are ready are named, so
 * nothing promises card payments while Stripe is pending.
 */
export type PaymentCopy = "none" | "crypto_only" | "card_only" | "card_and_crypto";

export function paymentCopy(payments: PublicPayments): PaymentCopy {
	if (payments.card && payments.crypto) return "card_and_crypto";
	if (payments.card) return "card_only";
	if (payments.crypto) return "crypto_only";
	return "none";
}

/** "1, 3 or 12" for the crypto prepay lengths. */
export function monthsPhrase(months: number[]): string {
	const sorted = [...new Set(months)].sort((a, b) => a - b).map(String);
	if (sorted.length <= 1) return sorted.join("");
	return `${sorted.slice(0, -1).join(", ")} or ${sorted[sorted.length - 1]}`;
}

/** The answer to "How will I pay?", naming only the methods that are ready. */
export function howToPay(catalog: PublicCatalog): string {
	const months = monthsPhrase(catalog.payments.cryptoMonthOptions) || "1, 3 or 12";
	if (!paymentsEnabled(catalog)) {
		return "Payments are not open yet. This page will list the ways to pay as soon as they are.";
	}
	switch (paymentCopy(catalog.payments)) {
		case "crypto_only":
			return `In cryptocurrency (Bitcoin or USDT), through our own payment server. Plans are paid ahead for ${months} months and do not renew on their own; credit packs are a single payment.`;
		case "card_only":
			return "By card, through Stripe. Plans renew each month until you cancel; credit packs are a single payment.";
		default:
			return `By card, with plans renewing each month until you cancel, or in cryptocurrency (Bitcoin or USDT) for ${months} months paid ahead. Credit packs are a single payment either way.`;
	}
}

function coercePayments(raw: unknown): PublicPayments {
	if (!isRecord(raw)) return NO_PAYMENTS;
	return {
		card: raw.card === true,
		crypto: raw.crypto === true,
		cryptoMonthOptions: Array.isArray(raw.cryptoMonthOptions)
			? raw.cryptoMonthOptions.filter(
					(m): m is number => typeof m === "number" && Number.isInteger(m) && m > 0
				)
			: [],
	};
}

export function formatPrice(cents: number, currency = "USD", locale = "en-US"): string {
	return new Intl.NumberFormat(locale, {
		style: "currency",
		currency,
		minimumFractionDigits: cents % 100 === 0 ? 0 : 2,
		maximumFractionDigits: 2,
	}).format(cents / 100);
}

export function formatCredits(credits: number, locale = "en-US"): string {
	return new Intl.NumberFormat(locale).format(credits);
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Accept a catalog from the API only if it has the shape the page needs.
 * Anything else returns null and the caller keeps the defaults.
 */
export function coerceCatalog(raw: unknown): PublicCatalog | null {
	if (!isRecord(raw)) return null;
	const { plans, packs, prices } = raw;
	if (!Array.isArray(plans) || plans.length === 0) return null;
	const okPlans = plans.every(
		(p) =>
			isRecord(p) &&
			typeof p.slug === "string" &&
			typeof p.name === "string" &&
			typeof p.priceCents === "number" &&
			typeof p.monthlyCredits === "number"
	);
	if (!okPlans) return null;
	return {
		mode: parseBillingMode(raw.mode),
		currency: typeof raw.currency === "string" ? raw.currency : "USD",
		// A catalog without `payments` (an older backend) offers nothing.
		payments: coercePayments(raw.payments),
		plans: (plans as Record<string, unknown>[]).map((p) => ({
			slug: p.slug as string,
			name: p.name as string,
			tagline: typeof p.tagline === "string" ? p.tagline : "",
			priceCents: p.priceCents as number,
			monthlyCredits: p.monthlyCredits as number,
			features: Array.isArray(p.features)
				? p.features.filter((f): f is string => typeof f === "string")
				: [],
			highlighted: p.highlighted === true,
		})),
		packs: Array.isArray(packs)
			? (packs as unknown[]).filter(
					(k): k is PublicPack =>
						isRecord(k) &&
						typeof k.slug === "string" &&
						typeof k.name === "string" &&
						typeof k.credits === "number" &&
						typeof k.priceCents === "number"
				)
			: DEFAULT_CATALOG.packs,
		prices: Array.isArray(prices)
			? (prices as unknown[]).filter(
					(a): a is PublicActionPrice =>
						isRecord(a) &&
						typeof a.action === "string" &&
						typeof a.label === "string" &&
						typeof a.credits === "number" &&
						typeof a.unit === "string"
				)
			: DEFAULT_CATALOG.prices,
	};
}
