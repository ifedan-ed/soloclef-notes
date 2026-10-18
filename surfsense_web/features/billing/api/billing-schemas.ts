import { z } from "zod";

/**
 * Shapes of the billing API (`/api/v1/billing/*`). Credits are display units
 * (1 credit = 1 US cent); `*_cents` are amounts in the currency's minor unit.
 * Names, taglines, features, labels and pack names arrive localized from the
 * server (the request carries `X-Locale`).
 */

export const billingModes = ["off", "shadow", "live"] as const;
export type BillingMode = (typeof billingModes)[number];

export const entitlementsSchema = z.object({
	parsers: z.array(z.string()).default([]),
	vision: z.boolean().default(false),
	model_tiers: z.array(z.string()).default([]),
	studio_kinds: z.array(z.string()).default([]),
	max_video_seconds: z.number().default(0),
	max_pages_per_upload: z.number().default(0),
	max_concurrent_jobs: z.number().default(0),
	max_notebooks: z.number().default(0),
	max_sources_per_notebook: z.number().default(0),
});
export type Entitlements = z.infer<typeof entitlementsSchema>;

export const planSchema = z.object({
	id: z.number(),
	slug: z.string(),
	name: z.string(),
	tagline: z.string().default(""),
	features: z.array(z.string()).default([]),
	price_cents: z.number(),
	currency: z.string(),
	monthly_credits: z.number(),
	entitlements: entitlementsSchema,
	is_default: z.boolean(),
	public: z.boolean(),
	active: z.boolean(),
	sort_order: z.number(),
});
export type Plan = z.infer<typeof planSchema>;

export const priceSchema = z.object({
	action: z.string(),
	credits: z.number(),
	unit: z.string(),
	label: z.string(),
	sort_order: z.number(),
});
export type Price = z.infer<typeof priceSchema>;

export const packSchema = z.object({
	id: z.number(),
	slug: z.string(),
	name: z.string(),
	credits: z.number(),
	price_cents: z.number(),
	currency: z.string(),
	active: z.boolean(),
	sort_order: z.number(),
});
export type Pack = z.infer<typeof packSchema>;

export const catalogSchema = z.object({
	plans: z.array(planSchema),
	prices: z.array(priceSchema),
	packs: z.array(packSchema),
});
export type Catalog = z.infer<typeof catalogSchema>;

export const planSources = ["default", "stripe", "crypto", "admin"] as const;
export type PlanSource = (typeof planSources)[number];

export const accountSchema = z.object({
	plan_id: z.number().nullish(),
	plan_source: z.string(),
	plan_status: z.string().nullish(),
	period_start: z.string().nullish(),
	period_end: z.string().nullish(),
	paid_until: z.string().nullish(),
	cancel_at_period_end: z.boolean().default(false),
	has_subscription: z.boolean().default(false),
	exempt: z.boolean().default(false),
});
export type BillingAccount = z.infer<typeof accountSchema>;

export const balancesSchema = z.object({
	plan_credits: z.number(),
	plan_allowance: z.number(),
	topup_credits: z.number(),
	reserved_credits: z.number(),
	spendable_credits: z.number(),
	period_used_credits: z.number(),
});
export type Balances = z.infer<typeof balancesSchema>;

/** BTCPay payment method ids the server can accept (the admin picks which are on). */
export const cryptoMethods = [
	"BTC-CHAIN",
	"USDT-TRON",
	"USDT-POLYGON",
	"USDT-ETHEREUM",
	"USDT-BSC",
] as const;
export type CryptoMethod = (typeof cryptoMethods)[number];

export const paymentOptionsSchema = z.object({
	card: z.boolean(),
	crypto: z.object({
		enabled: z.boolean(),
		methods: z.array(z.string()),
		month_options: z.array(z.number()),
		discounts: z.record(z.string(), z.number()),
	}),
});
export type PaymentOptions = z.infer<typeof paymentOptionsSchema>;

export const billingMeSchema = catalogSchema.extend({
	mode: z.enum(billingModes),
	is_admin: z.boolean(),
	exempt: z.boolean(),
	plan: planSchema.nullable(),
	account: accountSchema,
	balances: balancesSchema,
	low_balance_credits: z.number().nullish(),
	payments: paymentOptionsSchema,
});
export type BillingMe = z.infer<typeof billingMeSchema>;

export const usageRowSchema = z.object({
	id: z.number(),
	action: z.string(),
	units: z.number(),
	credits: z.number(),
	mode: z.string(),
	status: z.string(),
	denial_code: z.string().nullish(),
	workspace_id: z.number().nullish(),
	notebook_id: z.number().nullish(),
	created_at: z.string(),
});
export type UsageRow = z.infer<typeof usageRowSchema>;

export const usagePageSchema = z.object({
	items: z.array(usageRowSchema),
	total: z.number(),
	by_action: z.array(z.object({ action: z.string(), count: z.number(), credits: z.number() })),
});
export type UsagePage = z.infer<typeof usagePageSchema>;

export const cryptoDetailsSchema = z.object({
	pay_address: z.string().nullish(),
	pay_amount: z.string().nullish(),
	pay_currency: z.string().nullish(),
	network: z.string().nullish(),
	actually_paid: z.string().nullish(),
	remaining: z.string().nullish(),
	expires_at: z.string().nullish(),
	payment_url: z.string().nullish(),
});
export type CryptoDetails = z.infer<typeof cryptoDetailsSchema>;

export const paymentSchema = z.object({
	id: z.number(),
	provider: z.string(),
	kind: z.string(),
	status: z.string(),
	amount_cents: z.number(),
	currency: z.string(),
	credits: z.number(),
	plan_id: z.number().nullish(),
	months: z.number().nullish(),
	receipt_url: z.string().nullish(),
	created_at: z.string(),
	completed_at: z.string().nullish(),
	crypto: cryptoDetailsSchema.nullish(),
});
export type Payment = z.infer<typeof paymentSchema>;

export const paymentListSchema = z.object({ items: z.array(paymentSchema) });

export const urlSchema = z.object({ url: z.string() });
export const okSchema = z.object({ ok: z.boolean() });

export type CheckoutRequest = { kind: "plan"; plan_id: number } | { kind: "pack"; pack_id: number };

export type CryptoPaymentRequest = CheckoutRequest & {
	months?: number;
	/** A BTCPay payment method id; left out, the buyer chooses on BTCPay's checkout. */
	pay_currency?: string;
};
