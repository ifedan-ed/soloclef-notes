import type { BillingAccount, BillingMe, PaymentOptions, Plan } from "../api/billing-schemas";

export type PlanRelation = "current" | "upgrade" | "downgrade";

/** Where a plan sits relative to the one the person is on, by price then order. */
export function planRelation(plan: Plan, current: Plan | null): PlanRelation {
	if (current && plan.id === current.id) return "current";
	if (!current) return plan.price_cents > 0 ? "upgrade" : "downgrade";
	if (plan.price_cents !== current.price_cents) {
		return plan.price_cents > current.price_cents ? "upgrade" : "downgrade";
	}
	return plan.sort_order > current.sort_order ? "upgrade" : "downgrade";
}

export function isFreePlan(plan: Plan): boolean {
	return plan.is_default || plan.price_cents <= 0;
}

/**
 * What choosing a plan does:
 * - `none`: nothing to buy (the current plan, or free while not subscribed)
 * - `change`: the card subscription is switched (free = cancel at period end)
 * - `buy`: a new purchase, by card checkout or crypto prepay
 */
export type PlanChoice = "none" | "change" | "buy";

export function planChoice(plan: Plan, current: Plan | null, account: BillingAccount): PlanChoice {
	if (planRelation(plan, current) === "current") return "none";
	if (account.has_subscription) {
		// Switching an active subscription back to free is a cancellation.
		if (isFreePlan(plan) && account.cancel_at_period_end) return "none";
		return "change";
	}
	return isFreePlan(plan) ? "none" : "buy";
}

export type PaymentMethod = "card" | "crypto";

/** Payment methods that can buy this plan or pack right now. */
export function availableMethods(
	payments: PaymentOptions,
	target: { kind: "plan"; hasSubscription: boolean } | { kind: "pack" }
): PaymentMethod[] {
	const methods: PaymentMethod[] = [];
	if (payments.card) methods.push("card");
	// Crypto prepays plan months; a live card subscription must be cancelled first.
	const cryptoBlocked = target.kind === "plan" && target.hasSubscription;
	if (payments.crypto.enabled && !cryptoBlocked) {
		methods.push("crypto");
	}
	return methods;
}

export function paymentsAvailable(payments: PaymentOptions): boolean {
	return payments.card || payments.crypto.enabled;
}

/** The button a plan card shows: none when nothing can pay for that choice. */
export type PlanButton = "current" | "upgrade" | "downgrade" | "hidden";

export function planButton(
	plan: Plan,
	me: Pick<BillingMe, "plan" | "account" | "payments">
): PlanButton {
	const relation = planRelation(plan, me.plan);
	if (relation === "current") return "current";
	const choice = planChoice(plan, me.plan, me.account);
	if (choice === "none") return "hidden";
	// Switching or cancelling a card subscription goes through Stripe.
	if (choice === "change") return me.payments.card ? relation : "hidden";
	const methods = availableMethods(me.payments, {
		kind: "plan",
		hasSubscription: me.account.has_subscription,
	});
	return methods.length > 0 ? relation : "hidden";
}

export function canBuyPacks(payments: PaymentOptions): boolean {
	return availableMethods(payments, { kind: "pack" }).length > 0;
}

/**
 * How plans are paid for, which picks the copy above the plan cards:
 * `crypto_only` prepays 1, 3 or 12 months; `card` renews monthly (with crypto
 * as the prepaid alternative when it is on too); `none` while nothing is set up.
 */
export type PlanPaymentCopy = "none" | "crypto_only" | "card";

export function planPaymentCopy(payments: PaymentOptions): PlanPaymentCopy {
	if (payments.card) return "card";
	return payments.crypto.enabled ? "crypto_only" : "none";
}

/** "1, 3 or 12" style list of the prepaid lengths, for copy. */
export function monthList(months: number[], locale: string): string {
	const sorted = [...new Set(months)].sort((a, b) => a - b).map(String);
	try {
		return new Intl.ListFormat(locale, { style: "long", type: "disjunction" }).format(sorted);
	} catch {
		return sorted.join(", ");
	}
}

/** Percent off for prepaying this many months by crypto. */
export function cryptoDiscount(discounts: Record<string, number>, months: number): number {
	const pct = Number(discounts[String(months)] ?? 0);
	return Number.isFinite(pct) ? Math.min(100, Math.max(0, pct)) : 0;
}

/** Mirrors the server's crypto_price_cents: price x months, less the discount. */
export function cryptoPlanCents(
	priceCents: number,
	months: number,
	discounts: Record<string, number>
): number {
	return Math.round((priceCents * months * (100 - cryptoDiscount(discounts, months))) / 100);
}

export type PlanSourceKind = "free" | "card" | "crypto" | "admin";

/** How the current plan is held, for the "source" line under its name. */
export function planSourceKind(me: Pick<BillingMe, "account">): PlanSourceKind {
	switch (me.account.plan_source) {
		case "stripe":
			return "card";
		case "crypto":
			return "crypto";
		case "admin":
			return "admin";
		default:
			return "free";
	}
}

/** The date that ends the current period: crypto/admin plans end at paid_until. */
export function periodEnd(account: BillingAccount): string | null {
	if (account.plan_source === "crypto" || account.plan_source === "admin") {
		return account.paid_until ?? account.period_end ?? null;
	}
	return account.period_end ?? null;
}

/** Credits a studio kind costs, from the price table (`studio_<kind>`). */
export function priceFor(prices: { action: string; credits: number }[], action: string) {
	return prices.find((price) => price.action === action);
}

/** True when the plan's entitlements let it start this studio kind. */
export function planAllowsStudioKind(plan: Plan | null, kind: string): boolean {
	if (!plan) return true;
	return plan.entitlements.studio_kinds.includes(kind);
}
