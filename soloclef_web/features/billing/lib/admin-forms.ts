import type {
	AdminPack,
	AdminPlan,
	LocalizedContent,
	PackBody,
	PlanBody,
	SettingsValues,
} from "../api/billing-admin-schemas";
import type { Entitlements } from "../api/billing-schemas";
import {
	centsToDollars,
	creditsToMicros,
	dollarsToCents,
	microsToCredits,
	parseNumber,
} from "./format";

/** Locales that plan, price and pack content can be written in; English is required. */
export const CONTENT_LOCALES = ["en", "es", "pt", "hi", "zh", "ko"] as const;
export type ContentLocale = (typeof CONTENT_LOCALES)[number];

// ---------------------------------------------------------------- settings

/** The settings form as strings, the way inputs hold them. */
export type SettingsDraft = {
	stripe_enabled: boolean;
	stripe_publishable_key: string;
	crypto_enabled: boolean;
	btcpay_url: string;
	btcpay_store_id: string;
	crypto_payment_methods: string[];
	crypto_month_options: string;
	crypto_discounts: Record<string, string>;
	signup_bonus_credits: string;
	low_balance_credits: string;
	stale_hold_hours: string;
	support_email: string;
};

export function settingsToDraft(values: SettingsValues): SettingsDraft {
	return {
		stripe_enabled: values.stripe_enabled,
		stripe_publishable_key: values.stripe_publishable_key ?? "",
		crypto_enabled: values.crypto_enabled,
		btcpay_url: values.btcpay_url ?? "",
		btcpay_store_id: values.btcpay_store_id ?? "",
		crypto_payment_methods: [...values.crypto_payment_methods],
		crypto_month_options: values.crypto_month_options.join(", "),
		crypto_discounts: Object.fromEntries(
			Object.entries(values.crypto_discounts).map(([k, v]) => [k, String(v)])
		),
		signup_bonus_credits: String(microsToCredits(values.signup_bonus_micros)),
		low_balance_credits: String(values.low_balance_credits),
		stale_hold_hours: String(values.stale_hold_hours),
		support_email: values.support_email ?? "",
	};
}

/** "1, 3, 12" -> [1, 3, 12]: whole positive months, deduplicated and sorted. */
export function parseMonthOptions(text: string): number[] {
	const months = text
		.split(/[\s,;]+/)
		.map((part) => Number.parseInt(part, 10))
		.filter((n) => Number.isInteger(n) && n >= 1 && n <= 36);
	return [...new Set(months)].sort((a, b) => a - b);
}

export function draftToValues(draft: SettingsDraft): Partial<SettingsValues> {
	const months = parseMonthOptions(draft.crypto_month_options);
	const discounts: Record<string, number> = {};
	for (const m of months) {
		const pct = parseNumber(draft.crypto_discounts[String(m)] ?? "", 0);
		if (pct > 0) discounts[String(m)] = Math.min(100, pct);
	}
	return {
		stripe_enabled: draft.stripe_enabled,
		stripe_publishable_key: draft.stripe_publishable_key.trim(),
		crypto_enabled: draft.crypto_enabled,
		btcpay_url: draft.btcpay_url.trim().replace(/\/+$/, ""),
		btcpay_store_id: draft.btcpay_store_id.trim(),
		crypto_payment_methods: draft.crypto_payment_methods,
		crypto_month_options: months.length ? months : [1],
		crypto_discounts: discounts,
		signup_bonus_micros: creditsToMicros(Math.max(0, parseNumber(draft.signup_bonus_credits))),
		low_balance_credits: Math.max(0, parseNumber(draft.low_balance_credits)),
		stale_hold_hours: Math.max(1, Math.round(parseNumber(draft.stale_hold_hours, 6))),
		// Blank keeps the saved address: the backend refuses anything but one address.
		...(draft.support_email.trim() ? { support_email: draft.support_email.trim() } : {}),
	};
}

// ---------------------------------------------------------------- plans

export type PlanContentDraft = { name: string; tagline: string; features: string };

export type PlanDraft = {
	slug: string;
	sort_order: string;
	active: boolean;
	public: boolean;
	is_default: boolean;
	price: string;
	currency: string;
	monthly_credits: string;
	parsers: string[];
	vision: boolean;
	model_tiers: string[];
	studio_kinds: string[];
	max_video_seconds: string;
	max_pages_per_upload: string;
	max_concurrent_jobs: string;
	max_notebooks: string;
	max_sources_per_notebook: string;
	content: Record<string, PlanContentDraft>;
};

function text(value: unknown): string {
	return typeof value === "string" ? value : "";
}

function contentDraft(content: LocalizedContent): Record<string, PlanContentDraft> {
	const out: Record<string, PlanContentDraft> = {};
	for (const locale of CONTENT_LOCALES) {
		const row = content[locale] ?? {};
		const features = Array.isArray(row.features) ? row.features.map(String) : [];
		out[locale] = {
			name: text(row.name),
			tagline: text(row.tagline),
			features: features.join("\n"),
		};
	}
	return out;
}

export function emptyPlanDraft(sortOrder = 0): PlanDraft {
	return {
		slug: "",
		sort_order: String(sortOrder),
		active: true,
		public: true,
		is_default: false,
		price: "0.00",
		currency: "usd",
		monthly_credits: "0",
		parsers: ["local"],
		vision: false,
		model_tiers: ["standard"],
		studio_kinds: [],
		max_video_seconds: "0",
		max_pages_per_upload: "0",
		max_concurrent_jobs: "1",
		max_notebooks: "0",
		max_sources_per_notebook: "0",
		content: contentDraft({}),
	};
}

export function planToDraft(plan: AdminPlan): PlanDraft {
	const e: Entitlements = plan.entitlements;
	return {
		slug: plan.slug,
		sort_order: String(plan.sort_order),
		active: plan.active,
		public: plan.public,
		is_default: plan.is_default,
		price: centsToDollars(plan.price_cents),
		currency: plan.currency,
		monthly_credits: String(plan.monthly_credits),
		parsers: [...e.parsers],
		vision: e.vision,
		model_tiers: [...e.model_tiers],
		studio_kinds: [...e.studio_kinds],
		max_video_seconds: String(e.max_video_seconds),
		max_pages_per_upload: String(e.max_pages_per_upload),
		max_concurrent_jobs: String(e.max_concurrent_jobs),
		max_notebooks: String(e.max_notebooks),
		max_sources_per_notebook: String(e.max_sources_per_notebook),
		content: contentDraft(plan.content),
	};
}

/** Content with blank locales dropped, so the server falls back to English for them. */
function planContent(content: Record<string, PlanContentDraft>): LocalizedContent {
	const out: LocalizedContent = {};
	for (const locale of CONTENT_LOCALES) {
		const row = content[locale];
		if (!row) continue;
		const features = row.features
			.split("\n")
			.map((line) => line.trim())
			.filter(Boolean);
		const entry: Record<string, unknown> = {};
		if (row.name.trim()) entry.name = row.name.trim();
		if (row.tagline.trim()) entry.tagline = row.tagline.trim();
		if (features.length) entry.features = features;
		if (Object.keys(entry).length) out[locale] = entry;
	}
	return out;
}

const wholeNumber = (value: string) => Math.max(0, Math.round(parseNumber(value)));

export function draftToPlanBody(draft: PlanDraft): PlanBody {
	return {
		slug: draft.slug.trim().toLowerCase(),
		sort_order: Math.round(parseNumber(draft.sort_order)),
		active: draft.active,
		public: draft.public,
		is_default: draft.is_default,
		price_cents: Math.max(0, dollarsToCents(draft.price)),
		currency: draft.currency.trim().toLowerCase() || "usd",
		monthly_credits: Math.max(0, parseNumber(draft.monthly_credits)),
		entitlements: {
			parsers: draft.parsers,
			vision: draft.vision,
			model_tiers: draft.model_tiers,
			studio_kinds: draft.studio_kinds,
			max_video_seconds: wholeNumber(draft.max_video_seconds),
			max_pages_per_upload: wholeNumber(draft.max_pages_per_upload),
			max_concurrent_jobs: wholeNumber(draft.max_concurrent_jobs),
			max_notebooks: wholeNumber(draft.max_notebooks),
			max_sources_per_notebook: wholeNumber(draft.max_sources_per_notebook),
		},
		content: planContent(draft.content),
	};
}

export const SLUG_PATTERN = /^[a-z0-9-]+$/;

export type PlanDraftProblem = "slug" | "name_en";

/** What stops a plan from saving: a bad slug, or no English name. */
export function planDraftProblems(draft: PlanDraft): PlanDraftProblem[] {
	const problems: PlanDraftProblem[] = [];
	if (!SLUG_PATTERN.test(draft.slug.trim().toLowerCase())) problems.push("slug");
	if (!draft.content.en?.name.trim()) problems.push("name_en");
	return problems;
}

/** Toggle membership of a value in a list (checkbox groups). */
export function toggle(list: string[], value: string, on: boolean): string[] {
	return on ? (list.includes(value) ? list : [...list, value]) : list.filter((v) => v !== value);
}

// ---------------------------------------------------------------- packs

export type PackDraft = {
	slug: string;
	active: boolean;
	sort_order: string;
	credits: string;
	price: string;
	currency: string;
	names: Record<string, string>;
};

export function emptyPackDraft(sortOrder = 0): PackDraft {
	return {
		slug: "",
		active: true,
		sort_order: String(sortOrder),
		credits: "",
		price: "",
		currency: "usd",
		names: Object.fromEntries(CONTENT_LOCALES.map((l) => [l, ""])),
	};
}

export function packToDraft(pack: AdminPack): PackDraft {
	return {
		slug: pack.slug,
		active: pack.active,
		sort_order: String(pack.sort_order),
		credits: String(pack.credits),
		price: centsToDollars(pack.price_cents),
		currency: pack.currency,
		names: Object.fromEntries(CONTENT_LOCALES.map((l) => [l, text(pack.content[l]?.name)])),
	};
}

export function draftToPackBody(draft: PackDraft): PackBody {
	const content: LocalizedContent = {};
	for (const locale of CONTENT_LOCALES) {
		const name = (draft.names[locale] ?? "").trim();
		if (name) content[locale] = { name };
	}
	return {
		slug: draft.slug.trim().toLowerCase(),
		active: draft.active,
		sort_order: Math.round(parseNumber(draft.sort_order)),
		credits: Math.max(0, parseNumber(draft.credits)),
		price_cents: Math.max(0, dollarsToCents(draft.price)),
		currency: draft.currency.trim().toLowerCase() || "usd",
		content,
	};
}

export function packDraftValid(draft: PackDraft): boolean {
	const body = draftToPackBody(draft);
	return (
		SLUG_PATTERN.test(body.slug) &&
		body.credits > 0 &&
		body.price_cents > 0 &&
		!!body.content.en?.name
	);
}

// ---------------------------------------------------------------- prices

/** Per-locale labels for a price, blanks dropped. */
export function priceContent(labels: Record<string, string>): LocalizedContent {
	const out: LocalizedContent = {};
	for (const locale of CONTENT_LOCALES) {
		const label = (labels[locale] ?? "").trim();
		if (label) out[locale] = { label };
	}
	return out;
}

export function priceLabels(content: LocalizedContent): Record<string, string> {
	return Object.fromEntries(CONTENT_LOCALES.map((l) => [l, text(content[l]?.label)]));
}

// ---------------------------------------------------------------- adjustments

/** Whether a manual adjustment can be sent: a non-zero amount and a 3-300 character reason. */
export function adjustmentValid(amount: string, reason: string): boolean {
	const value = parseNumber(amount, 0);
	const why = reason.trim();
	return value !== 0 && why.length >= 3 && why.length <= 300;
}

/** Where a payment method stands, for its badge on the admin page. */
export type MethodState = "ready" | "pending" | "off";

export function methodState(status: {
	ready: boolean;
	enabled: boolean;
	missing: string[];
}): MethodState {
	if (status.ready) return "ready";
	// Switched off with nothing missing is a deliberate pause; anything
	// still missing means the method is not set up yet.
	return status.missing.length === 0 && !status.enabled ? "off" : "pending";
}

/** The message key naming a missing setting (a secret or a plain field). */
export function missingSettingKey(setting: string): string {
	if (setting === "btcpay_url" || setting === "btcpay_store_id") return `admin.${setting}`;
	return `admin.secrets.${setting}`;
}
