import { z } from "zod";
import {
	accountSchema,
	billingModes,
	packSchema,
	planSchema,
	priceSchema,
} from "./billing-schemas";

/** Shapes of `/api/v1/billing/admin/*`. Secrets are write-only: only set/unset comes back. */

export const localizedContentSchema = z.record(z.string(), z.record(z.string(), z.unknown()));
export type LocalizedContent = Record<string, Record<string, unknown>>;

export const adminPlanSchema = planSchema.extend({
	content: localizedContentSchema,
	stripe_price_id: z.string().nullish(),
});
export type AdminPlan = z.infer<typeof adminPlanSchema>;

export const adminPriceSchema = priceSchema.extend({ content: localizedContentSchema });
export type AdminPrice = z.infer<typeof adminPriceSchema>;

export const adminPackSchema = packSchema.extend({ content: localizedContentSchema });
export type AdminPack = z.infer<typeof adminPackSchema>;

export const secretStatusSchema = z.object({
	set: z.boolean(),
	source: z.enum(["admin", "env"]).nullable(),
});
export type SecretStatus = z.infer<typeof secretStatusSchema>;

export const secretKeys = [
	"stripe_secret_key",
	"stripe_webhook_secret",
	"btcpay_api_key",
	"btcpay_webhook_secret",
] as const;
export type SecretKey = (typeof secretKeys)[number];

export const settingsValuesSchema = z.object({
	mode: z.enum(billingModes),
	signup_bonus_micros: z.number(),
	stripe_enabled: z.boolean(),
	stripe_publishable_key: z.string().nullish(),
	crypto_enabled: z.boolean(),
	crypto_provider: z.string().nullish(),
	btcpay_url: z.string().nullish(),
	btcpay_store_id: z.string().nullish(),
	crypto_payment_methods: z.array(z.string()),
	crypto_month_options: z.array(z.number()),
	crypto_discounts: z.record(z.string(), z.number()),
	stale_hold_hours: z.number(),
	model_tiers: z.record(z.string(), z.string()),
	low_balance_credits: z.number(),
	support_email: z.string().nullish(),
});
export type SettingsValues = z.infer<typeof settingsValuesSchema>;

/** One payment method: offered to people only when `ready`. */
export const methodStatusSchema = z.object({
	ready: z.boolean(),
	enabled: z.boolean(),
	missing: z.array(z.string()),
});
export type MethodStatus = z.infer<typeof methodStatusSchema>;

export const settingsDescribeSchema = z.object({
	values: settingsValuesSchema,
	secrets: z.record(z.string(), secretStatusSchema),
	stripe_mode: z.enum(["test", "live"]).nullable(),
	payments: z.object({ card: methodStatusSchema, crypto: methodStatusSchema }).optional(),
});
export type SettingsDescribe = z.infer<typeof settingsDescribeSchema>;

export const adminOverviewSchema = z.object({
	settings: settingsDescribeSchema,
	secret_keys: z.array(z.string()),
	plans: z.array(adminPlanSchema),
	prices: z.array(adminPriceSchema),
	packs: z.array(adminPackSchema),
	choices: z.object({
		studio_kinds: z.array(z.string()),
		parsers: z.array(z.string()),
		model_tiers: z.array(z.string()),
	}),
	webhooks: z.object({ stripe: z.string(), crypto: z.string() }),
});
export type AdminOverview = z.infer<typeof adminOverviewSchema>;

export type SettingsUpdate = {
	values?: Partial<SettingsValues>;
	/** key -> new value; "" clears it. Keys left out are unchanged. */
	secrets?: Partial<Record<SecretKey, string>>;
};

export type PlanBody = {
	slug: string;
	sort_order: number;
	active: boolean;
	public: boolean;
	is_default: boolean;
	price_cents: number;
	currency: string;
	monthly_credits: number;
	entitlements: Record<string, unknown>;
	content: LocalizedContent;
};

export type PriceBody = { credits: number; unit?: string; content?: LocalizedContent };

export type PackBody = {
	slug: string;
	active: boolean;
	sort_order: number;
	credits: number;
	price_cents: number;
	currency: string;
	content: LocalizedContent;
};

export const adminUserSchema = z.object({
	user_id: z.string(),
	email: z.string(),
	is_config_admin: z.boolean(),
	topup_credits: z.number(),
	plan_credits: z.number().nullable(),
	account: accountSchema.nullable(),
});
export type AdminUser = z.infer<typeof adminUserSchema>;
export const adminUserListSchema = z.object({ items: z.array(adminUserSchema) });

export type AccountUpdate = {
	plan_id?: number;
	paid_until?: string | null;
	exempt?: boolean;
	exempt_reason?: string;
};

export type AdjustBody = { credits: number; bucket: "topup" | "plan"; reason: string };

export const ledgerEntrySchema = z.object({
	id: z.number(),
	bucket: z.string(),
	credits: z.number(),
	balance_after: z.number(),
	kind: z.string(),
	reason: z.string().nullish(),
	created_at: z.string(),
});
export type LedgerEntry = z.infer<typeof ledgerEntrySchema>;
export const ledgerSchema = z.object({ items: z.array(ledgerEntrySchema) });

export const modelTiers = ["standard", "premium"] as const;
export type ModelTier = (typeof modelTiers)[number];

export const adminModelSchema = z.object({
	model_id: z.string(),
	display_name: z.string(),
	source: z.string(),
	tier: z.enum(modelTiers),
});
export type AdminModel = z.infer<typeof adminModelSchema>;
export const adminModelListSchema = z.object({ items: z.array(adminModelSchema) });

export const reportSchema = z.object({
	days: z.number(),
	by_action: z.array(
		z.object({
			action: z.string(),
			mode: z.string(),
			count: z.number(),
			units: z.number(),
			credits: z.number(),
			would_refuse: z.number(),
			provider_cost_usd: z.number(),
		})
	),
	by_user: z.array(z.object({ email: z.string(), count: z.number(), credits: z.number() })),
	denials: z.array(z.object({ code: z.string().nullable(), count: z.number() })),
});
export type UsageReport = z.infer<typeof reportSchema>;
