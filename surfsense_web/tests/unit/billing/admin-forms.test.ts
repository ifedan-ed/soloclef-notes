import assert from "node:assert/strict";
import test from "node:test";
import type {
	AdminPack,
	AdminPlan,
	SettingsValues,
} from "@/features/billing/api/billing-admin-schemas";
import {
	adjustmentValid,
	draftToPackBody,
	draftToPlanBody,
	draftToValues,
	emptyPackDraft,
	emptyPlanDraft,
	methodState,
	missingSettingKey,
	packDraftValid,
	packToDraft,
	parseMonthOptions,
	planDraftProblems,
	planToDraft,
	priceContent,
	priceLabels,
	settingsToDraft,
	toggle,
} from "@/features/billing/lib/admin-forms";
import { PLUS } from "./fixtures";

const SETTINGS: SettingsValues = {
	mode: "shadow",
	signup_bonus_micros: 500_000,
	stripe_enabled: false,
	stripe_publishable_key: "pk_test_1",
	crypto_enabled: true,
	crypto_provider: "btcpay",
	btcpay_url: "https://pay.example.com",
	btcpay_store_id: "store-1",
	crypto_payment_methods: ["BTC-CHAIN", "USDT-TRON"],
	crypto_month_options: [1, 3, 12],
	crypto_discounts: { "12": 10 },
	stale_hold_hours: 6,
	model_tiers: {},
	low_balance_credits: 20,
	support_email: "admin@soloclef.com",
};

test("settings round-trip through the form, with the bonus in credits", () => {
	const draft = settingsToDraft(SETTINGS);
	assert.equal(draft.signup_bonus_credits, "50");
	assert.equal(draft.crypto_month_options, "1, 3, 12");
	const values = draftToValues(draft);
	assert.equal(values.signup_bonus_micros, 500_000);
	assert.deepEqual(values.crypto_month_options, [1, 3, 12]);
	assert.deepEqual(values.crypto_discounts, { "12": 10 });
	assert.equal(values.stale_hold_hours, 6);
	assert.equal(values.support_email, "admin@soloclef.com");
	assert.equal(values.btcpay_url, "https://pay.example.com");
	assert.equal(values.btcpay_store_id, "store-1");
	assert.deepEqual(values.crypto_payment_methods, ["BTC-CHAIN", "USDT-TRON"]);
	for (const gone of ["crypto_currencies", "crypto_underpay_tolerance_percent", "oxapay_sandbox"]) {
		assert.equal(gone in values, false, gone);
	}
	assert.equal("mode" in values, false, "the mode is saved on its own, behind a confirmation");
});

test("the BTCPay URL and store id are trimmed", () => {
	const draft = {
		...settingsToDraft(SETTINGS),
		btcpay_url: " https://pay.example.com/ ",
		btcpay_store_id: " abc ",
	};
	const values = draftToValues(draft);
	assert.equal(values.btcpay_url, "https://pay.example.com");
	assert.equal(values.btcpay_store_id, "abc");
});

test("month options are whole, deduplicated, sorted and bounded", () => {
	assert.deepEqual(parseMonthOptions("12, 1;3 3 x 0 40"), [1, 3, 12]);
	assert.deepEqual(parseMonthOptions(""), []);
	// Discounts for months no longer offered are dropped; none offered falls back to 1.
	const draft = {
		...settingsToDraft(SETTINGS),
		crypto_month_options: "",
		crypto_discounts: { "12": "5" },
	};
	const values = draftToValues(draft);
	assert.deepEqual(values.crypto_month_options, [1]);
	assert.deepEqual(values.crypto_discounts, {});
});

test("a plan edits every field and keeps only filled locales", () => {
	const admin: AdminPlan = {
		...PLUS,
		content: {
			en: { name: "Plus", tagline: "For study", features: ["A", "B"] },
			es: { name: "Plus ES" },
		},
		stripe_price_id: null,
	};
	const draft = planToDraft(admin);
	assert.equal(draft.price, "9.00");
	assert.equal(draft.content.en.features, "A\nB");
	assert.equal(draft.content.ko.name, "");
	draft.content.ko = { name: "플러스", tagline: "", features: " C \n\n" };
	draft.studio_kinds = toggle(draft.studio_kinds, "audio", true);
	draft.max_notebooks = "0";
	const body = draftToPlanBody(draft);
	assert.equal(body.price_cents, 900);
	assert.equal(body.monthly_credits, 1500);
	assert.deepEqual(body.content.en, { name: "Plus", tagline: "For study", features: ["A", "B"] });
	assert.deepEqual(body.content.es, { name: "Plus ES" });
	assert.deepEqual(body.content.ko, { name: "플러스", features: ["C"] });
	assert.equal("pt" in body.content, false);
	assert.ok((body.entitlements.studio_kinds as string[]).includes("audio"));
	assert.equal(body.entitlements.max_notebooks, 0);
});

test("a new plan needs a valid slug and an English name", () => {
	const draft = emptyPlanDraft(3);
	assert.deepEqual(planDraftProblems(draft), ["slug", "name_en"]);
	draft.slug = "Team Plan";
	draft.content.en.name = "Team";
	assert.deepEqual(planDraftProblems(draft), ["slug"]);
	draft.slug = "team-plan";
	assert.deepEqual(planDraftProblems(draft), []);
	assert.equal(draftToPlanBody(draft).sort_order, 3);
});

test("packs need a slug, credits, a price and an English name", () => {
	const draft = emptyPackDraft();
	assert.equal(packDraftValid(draft), false);
	Object.assign(draft, { slug: "credits-2000", credits: "2000", price: "15" });
	draft.names.en = "2,000 credits";
	assert.equal(packDraftValid(draft), true);
	const body = draftToPackBody(draft);
	assert.equal(body.price_cents, 1500);
	assert.deepEqual(body.content, { en: { name: "2,000 credits" } });

	const existing: AdminPack = {
		id: 1,
		slug: "credits-500",
		name: "500 credits",
		credits: 500,
		price_cents: 500,
		currency: "usd",
		active: true,
		sort_order: 0,
		content: { en: { name: "500 credits" }, zh: { name: "500 积分" } },
	};
	const back = draftToPackBody(packToDraft(existing));
	assert.deepEqual(back.content, existing.content);
	assert.equal(back.price_cents, 500);
});

test("price labels keep only filled locales", () => {
	const labels = priceLabels({ en: { label: "Quiz" }, hi: { label: "क्विज़" } });
	assert.equal(labels.en, "Quiz");
	assert.equal(labels.es, "");
	assert.deepEqual(priceContent({ ...labels, es: "  " }), {
		en: { label: "Quiz" },
		hi: { label: "क्विज़" },
	});
});

test("a manual adjustment needs a non-zero amount and a reason", () => {
	assert.equal(adjustmentValid("100", "Goodwill credit"), true);
	assert.equal(adjustmentValid("-25", "Refund reversal"), true);
	assert.equal(adjustmentValid("0", "Goodwill"), false);
	assert.equal(adjustmentValid("100", "ok"), false);
	assert.equal(adjustmentValid("100", "x".repeat(301)), false);
});

test("a payment method is Active, Pending (something missing) or Switched off", () => {
	assert.equal(methodState({ ready: true, enabled: true, missing: [] }), "ready");
	// Stripe today: no key, no webhook secret.
	assert.equal(
		methodState({
			ready: false,
			enabled: false,
			missing: ["stripe_secret_key", "stripe_webhook_secret"],
		}),
		"pending"
	);
	assert.equal(
		methodState({ ready: false, enabled: true, missing: ["btcpay_webhook_secret"] }),
		"pending"
	);
	assert.equal(methodState({ ready: false, enabled: false, missing: [] }), "off");
	assert.equal(missingSettingKey("btcpay_store_id"), "admin.btcpay_store_id");
	assert.equal(missingSettingKey("btcpay_api_key"), "admin.secrets.btcpay_api_key");
});

test("the Report Issue address is trimmed, and a blank one keeps the saved address", () => {
	const draft = settingsToDraft(SETTINGS);
	assert.equal(
		draftToValues({ ...draft, support_email: "  help@example.org " }).support_email,
		"help@example.org"
	);
	assert.equal("support_email" in draftToValues({ ...draft, support_email: "  " }), false);
	assert.equal(settingsToDraft({ ...SETTINGS, support_email: null }).support_email, "");
});
