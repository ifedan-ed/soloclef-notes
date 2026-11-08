import assert from "node:assert/strict";
import test from "node:test";
import {
	availableMethods,
	canBuyPacks,
	cryptoDiscount,
	cryptoPlanCents,
	monthList,
	paymentsAvailable,
	periodEnd,
	planAllowsStudioKind,
	planButton,
	planChoice,
	planPaymentCopy,
	planRelation,
	planSourceKind,
	priceFor,
} from "@/features/billing/lib/plans";
import { FREE, me, PLUS, PRO } from "./fixtures";

test("plans relate to the current one by price", () => {
	assert.equal(planRelation(FREE, FREE), "current");
	assert.equal(planRelation(PLUS, FREE), "upgrade");
	assert.equal(planRelation(FREE, PLUS), "downgrade");
	assert.equal(planRelation(PLUS, PRO), "downgrade");
	assert.equal(planRelation(PRO, null), "upgrade");
});

test("without a subscription, paid plans are bought and free is not offered", () => {
	const account = me().account;
	assert.equal(planChoice(PLUS, FREE, account), "buy");
	assert.equal(planChoice(FREE, FREE, account), "none");
	// On a crypto-prepaid Plus there is nothing to "buy" back to Free.
	assert.equal(planChoice(FREE, PLUS, { ...account, plan_source: "crypto" }), "none");
});

test("with a card subscription, every other plan is a change (free = cancel at period end)", () => {
	const account = { ...me().account, has_subscription: true, plan_source: "stripe" };
	assert.equal(planChoice(PRO, PLUS, account), "change");
	assert.equal(planChoice(FREE, PLUS, account), "change");
	assert.equal(planChoice(FREE, PLUS, { ...account, cancel_at_period_end: true }), "none");
	assert.equal(planChoice(PLUS, PLUS, account), "none");
});

test("payment methods follow what the server switched on", () => {
	const off = me().payments;
	assert.deepEqual(availableMethods(off, { kind: "pack" }), []);
	assert.equal(paymentsAvailable(off), false);

	const both = { card: true, crypto: { ...off.crypto, enabled: true } };
	assert.deepEqual(availableMethods(both, { kind: "pack" }), ["card", "crypto"]);
	assert.deepEqual(availableMethods(both, { kind: "plan", hasSubscription: false }), [
		"card",
		"crypto",
	]);
	// A live card subscription must be cancelled before prepaying plan months by crypto.
	assert.deepEqual(availableMethods(both, { kind: "plan", hasSubscription: true }), ["card"]);
	// With no method pinned, the buyer still chooses on BTCPay's checkout.
	const anyMethod = { card: false, crypto: { ...both.crypto, methods: [] } };
	assert.deepEqual(availableMethods(anyMethod, { kind: "pack" }), ["crypto"]);
	assert.equal(paymentsAvailable(anyMethod), true);
});

test("crypto plan prices match the server: price x months less the discount", () => {
	const discounts = { "12": 10, "3": 0 };
	assert.equal(cryptoDiscount(discounts, 12), 10);
	assert.equal(cryptoDiscount(discounts, 1), 0);
	assert.equal(cryptoPlanCents(900, 1, discounts), 900);
	assert.equal(cryptoPlanCents(900, 3, discounts), 2700);
	assert.equal(cryptoPlanCents(900, 12, discounts), 9720);
	assert.equal(cryptoDiscount({ "12": 250 }, 12), 100);
});

test("the plan's source and end date come from the account", () => {
	const base = me();
	assert.equal(planSourceKind(base), "free");
	assert.equal(planSourceKind({ account: { ...base.account, plan_source: "stripe" } }), "card");
	assert.equal(planSourceKind({ account: { ...base.account, plan_source: "crypto" } }), "crypto");
	assert.equal(planSourceKind({ account: { ...base.account, plan_source: "admin" } }), "admin");
	assert.equal(periodEnd(base.account), "2026-10-01T00:00:00Z");
	assert.equal(
		periodEnd({ ...base.account, plan_source: "crypto", paid_until: "2027-01-01T00:00:00Z" }),
		"2027-01-01T00:00:00Z"
	);
});

test("studio kinds and prices are read from the plan and the price table", () => {
	assert.equal(planAllowsStudioKind(FREE, "quiz"), true);
	assert.equal(planAllowsStudioKind(FREE, "video"), false);
	assert.equal(planAllowsStudioKind(PLUS, "video"), true);
	assert.equal(planAllowsStudioKind(null, "video"), true);
	assert.equal(priceFor(me().prices, "studio_quiz")?.credits, 5);
	assert.equal(priceFor(me().prices, "studio_video"), undefined);
});

test("plan buttons appear only when a ready method can pay for that choice", () => {
	const off = me();
	// Nothing set up: no Upgrade that would lead nowhere.
	assert.equal(planButton(FREE, off), "current");
	assert.equal(planButton(PLUS, off), "hidden");
	assert.equal(planButton(PRO, off), "hidden");
	assert.equal(canBuyPacks(off.payments), false);

	const cryptoOnly = me({
		payments: { ...off.payments, crypto: { ...off.payments.crypto, enabled: true } },
	});
	assert.equal(planButton(PLUS, cryptoOnly), "upgrade");
	assert.equal(canBuyPacks(cryptoOnly.payments), true);

	// A card subscriber can only switch plans through Stripe.
	const subscribed = {
		...off,
		plan: PLUS,
		account: { ...off.account, plan_id: PLUS.id, plan_source: "stripe", has_subscription: true },
	};
	assert.equal(planButton(PRO, { ...subscribed, payments: cryptoOnly.payments }), "hidden");
	assert.equal(
		planButton(PRO, { ...subscribed, payments: { ...off.payments, card: true } }),
		"upgrade"
	);
	assert.equal(
		planButton(FREE, { ...subscribed, payments: { ...off.payments, card: true } }),
		"downgrade"
	);
});

test("plan copy follows the methods: crypto-only prepays months", () => {
	const off = me().payments;
	assert.equal(planPaymentCopy(off), "none");
	assert.equal(
		planPaymentCopy({ ...off, crypto: { ...off.crypto, enabled: true } }),
		"crypto_only"
	);
	assert.equal(planPaymentCopy({ ...off, card: true }), "card");
	assert.equal(monthList([12, 1, 3], "en"), "1, 3, or 12");
});
