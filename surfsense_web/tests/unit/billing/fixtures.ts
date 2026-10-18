import type { BillingMe, Plan } from "@/features/billing/api/billing-schemas";

const entitlements = {
	parsers: ["local"],
	vision: false,
	model_tiers: ["standard"],
	studio_kinds: ["quiz", "report"],
	max_video_seconds: 0,
	max_pages_per_upload: 50,
	max_concurrent_jobs: 1,
	max_notebooks: 0,
	max_sources_per_notebook: 0,
};

export function plan(overrides: Partial<Plan> = {}): Plan {
	return {
		id: 1,
		slug: "free",
		name: "Free",
		tagline: "Try it out.",
		features: ["100 credits every month"],
		price_cents: 0,
		currency: "usd",
		monthly_credits: 100,
		entitlements,
		is_default: true,
		public: true,
		active: true,
		sort_order: 0,
		...overrides,
	};
}

export const FREE = plan();
export const PLUS = plan({
	id: 2,
	slug: "plus",
	name: "Plus",
	tagline: "For regular study.",
	features: ["1,500 credits every month", "Premium models"],
	price_cents: 900,
	monthly_credits: 1500,
	is_default: false,
	sort_order: 1,
	entitlements: { ...entitlements, studio_kinds: ["quiz", "report", "video"] },
});
export const PRO = plan({
	id: 3,
	slug: "pro",
	name: "Pro",
	price_cents: 2400,
	monthly_credits: 5000,
	is_default: false,
	sort_order: 2,
});

export function me(overrides: Partial<BillingMe> = {}): BillingMe {
	return {
		mode: "shadow",
		is_admin: false,
		exempt: false,
		plan: FREE,
		account: {
			plan_id: 1,
			plan_source: "default",
			plan_status: "active",
			period_start: "2026-09-01T00:00:00Z",
			period_end: "2026-10-01T00:00:00Z",
			paid_until: null,
			cancel_at_period_end: false,
			has_subscription: false,
			exempt: false,
		},
		balances: {
			plan_credits: 40,
			plan_allowance: 100,
			topup_credits: 250,
			reserved_credits: 0,
			spendable_credits: 290,
			period_used_credits: 60,
		},
		low_balance_credits: 20,
		payments: {
			card: false,
			crypto: {
				enabled: false,
				methods: ["BTC-CHAIN", "USDT-TRON", "USDT-POLYGON"],
				month_options: [1, 3, 12],
				discounts: { "12": 10 },
			},
		},
		plans: [FREE, PLUS, PRO],
		prices: [
			{ action: "studio_quiz", credits: 5, unit: "output", label: "Quiz", sort_order: 0 },
			{ action: "cloud_page", credits: 2, unit: "page", label: "Cloud parsing", sort_order: 1 },
			{ action: "local_page", credits: 0, unit: "page", label: "Local parsing", sort_order: 2 },
		],
		packs: [
			{
				id: 7,
				slug: "credits-500",
				name: "500 credits",
				credits: 500,
				price_cents: 500,
				currency: "usd",
				active: true,
				sort_order: 0,
			},
		],
		...overrides,
	};
}
