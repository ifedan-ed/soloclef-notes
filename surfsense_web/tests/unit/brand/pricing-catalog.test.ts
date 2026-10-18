import assert from "node:assert/strict";
import test from "node:test";
import {
	coerceCatalog,
	DEFAULT_CATALOG,
	formatCredits,
	formatPrice,
	howToPay,
	monthsPhrase,
	NO_PAYMENTS,
	type PublicCatalog,
	parseBillingMode,
	paymentCopy,
	paymentsEnabled,
} from "@/lib/pricing/catalog";

const CRYPTO_ONLY = { card: false, crypto: true, cryptoMonthOptions: [1, 3, 12] };
const withPayments = (mode: PublicCatalog["mode"], payments = NO_PAYMENTS): PublicCatalog => ({
	...DEFAULT_CATALOG,
	mode,
	payments,
});

test("the default catalog carries the owner-approved plans and packs", () => {
	const plans = Object.fromEntries(DEFAULT_CATALOG.plans.map((p) => [p.slug, p]));
	assert.deepEqual(Object.keys(plans), ["free", "plus", "pro"], "plans in order: free, plus, pro");
	assert.equal(plans.free.priceCents, 0);
	assert.equal(plans.free.monthlyCredits, 100);
	assert.equal(plans.plus.priceCents, 900);
	assert.equal(plans.plus.monthlyCredits, 1500);
	assert.equal(plans.pro.priceCents, 2400);
	assert.equal(plans.pro.monthlyCredits, 5000);
	const pack = DEFAULT_CATALOG.packs.find((p) => p.slug === "credits-500");
	assert.equal(pack?.credits, 500);
	assert.equal(pack?.priceCents, 500);
	const video = DEFAULT_CATALOG.prices.find((p) => p.action === "studio_video");
	assert.equal(video?.credits, 40);
	assert.equal(DEFAULT_CATALOG.mode, "shadow");
});

test("payments are offered only in live mode, and only with a method set up", () => {
	assert.equal(paymentsEnabled({ mode: "shadow", payments: CRYPTO_ONLY }), false);
	assert.equal(paymentsEnabled({ mode: "off", payments: CRYPTO_ONLY }), false);
	assert.equal(paymentsEnabled({ mode: "live", payments: CRYPTO_ONLY }), true);
	assert.equal(paymentsEnabled({ mode: "live", payments: NO_PAYMENTS }), false);
	assert.equal(paymentsEnabled({ mode: "live", payments: { ...NO_PAYMENTS, card: true } }), true);
	assert.deepEqual(DEFAULT_CATALOG.payments, NO_PAYMENTS, "the fallback offers nothing");
	assert.equal(parseBillingMode("live"), "live");
	assert.equal(parseBillingMode("LIVE"), "shadow", "unknown values fall back");
	assert.equal(parseBillingMode(undefined), "shadow");
	assert.equal(parseBillingMode("bogus", "off"), "off");
});

test("prices and credits format for people", () => {
	assert.equal(formatPrice(0), "$0");
	assert.equal(formatPrice(900), "$9");
	assert.equal(formatPrice(2400), "$24");
	assert.equal(formatPrice(499), "$4.99");
	assert.equal(formatCredits(1500), "1,500");
});

test("coerceCatalog accepts the API shape and rejects anything else", () => {
	assert.equal(coerceCatalog(null), null);
	assert.equal(coerceCatalog({ plans: [] }), null);
	assert.equal(coerceCatalog({ plans: [{ slug: "x" }] }), null);
	const got = coerceCatalog({
		mode: "live",
		currency: "EUR",
		plans: [
			{ slug: "solo", name: "Solo", priceCents: 500, monthlyCredits: 700, features: ["a", 3] },
		],
		packs: [{ slug: "p", name: "P", credits: 10, priceCents: 100 }, { bad: true }],
		prices: [{ action: "chat_standard", label: "Chat", credits: 1, unit: "turn" }],
	});
	assert.ok(got);
	assert.equal(got.mode, "live");
	assert.equal(got.currency, "EUR");
	assert.deepEqual(got.plans[0].features, ["a"]);
	assert.equal(got.plans[0].tagline, "");
	assert.equal(got.packs.length, 1);
	assert.equal(got.prices.length, 1);
	const partial = coerceCatalog({
		mode: "nope",
		plans: [{ slug: "a", name: "A", priceCents: 0, monthlyCredits: 1 }],
	});
	assert.equal(partial?.mode, "shadow");
	assert.deepEqual(partial?.payments, NO_PAYMENTS, "an older backend's catalog offers nothing");
	assert.deepEqual(
		coerceCatalog({
			plans: [{ slug: "a", name: "A", priceCents: 0, monthlyCredits: 1 }],
			payments: { card: "yes", crypto: true, cryptoMonthOptions: [1, 3, "12", 12, -1] },
		})?.payments,
		{ card: false, crypto: true, cryptoMonthOptions: [1, 3, 12] }
	);
	assert.deepEqual(partial?.packs, DEFAULT_CATALOG.packs, "missing packs keep the defaults");
});

test("the site names only the payment methods that are ready", () => {
	assert.equal(paymentCopy(NO_PAYMENTS), "none");
	assert.equal(paymentCopy(CRYPTO_ONLY), "crypto_only");
	assert.equal(paymentCopy({ ...NO_PAYMENTS, card: true }), "card_only");
	assert.equal(paymentCopy({ ...CRYPTO_ONLY, card: true }), "card_and_crypto");
	assert.equal(monthsPhrase([12, 1, 3]), "1, 3 or 12");
	assert.equal(monthsPhrase([1]), "1");

	// Stripe pending, crypto not finished: no method is promised at all.
	const none = howToPay(withPayments("live"));
	assert.match(none, /not open yet/);
	assert.doesNotMatch(none, /card|stripe|crypto/i);
	// Crypto only: prepaid months, and no card.
	const crypto = howToPay(withPayments("live", CRYPTO_ONLY));
	assert.match(crypto, /cryptocurrency/);
	assert.match(crypto, /1, 3 or 12 months/);
	assert.doesNotMatch(crypto, /card|stripe/i);
	// Shadow mode never offers a method, even one that is set up.
	assert.match(howToPay(withPayments("shadow", CRYPTO_ONLY)), /not open yet/);
	// Stripe turned on later: card is named.
	assert.match(howToPay(withPayments("live", { ...CRYPTO_ONLY, card: true })), /By card/);
});
