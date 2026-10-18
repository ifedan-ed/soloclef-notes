import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NextIntlClientProvider } from "next-intl";
import { createElement, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { BillingView } from "@/features/billing/ui/billing-view";
import { PaymentRow } from "@/features/billing/ui/payments-list";
import { UsageTable } from "@/features/billing/ui/usage-history";
import { me, PLUS } from "./fixtures";

function render(element: ReactElement, locale = "en"): string {
	const messages = JSON.parse(
		readFileSync(join(process.cwd(), "messages", `${locale}.json`), "utf8")
	);
	return renderToStaticMarkup(
		createElement(
			QueryClientProvider,
			{ client: new QueryClient() },
			createElement(NextIntlClientProvider, {
				locale,
				messages,
				timeZone: "UTC",
				// biome-ignore lint/correctness/noChildrenProp: the provider's props type requires children.
				children: element,
			})
		)
	);
}

const noop = () => undefined;
/** What a person reads: the markup without tags and attributes. */
const words = (html: string) => html.replace(/<[^>]*>/g, " ");
const view = (data = me(), locale = "en") =>
	render(createElement(BillingView, { me: data, onChoosePlan: noop, onBuyPack: noop }), locale);

test("shadow mode: the plan, credits, the measuring notice and payments not yet open", () => {
	const html = view();
	assert.match(html, /Current plan/);
	assert.match(html, /<h2 id="billing-current-plan"[^>]*>Free<\/h2>/);
	assert.match(html, /Free plan, no payment needed/);
	assert.match(html, /40 of 100 plan credits left this period/);
	assert.match(html, /aria-valuenow="40"/);
	assert.match(html, /Top-up balance/);
	assert.match(html, />250</);
	assert.match(html, /data-testid="billing-spendable"[^>]*>290</);
	assert.match(html, /Usage is being measured/);
	assert.match(html, /Nothing is charged yet/);
	assert.match(html, /Payments aren(’|'|&#x27;)t available yet/);
	// Plans and packs come from the API; with no payment method set up there
	// is nothing to click: no Upgrade and no Buy.
	assert.doesNotMatch(html, /aria-label="Upgrade: /);
	assert.doesNotMatch(html, /aria-label="Buy: /);
	assert.match(html, /\$9<span[^>]*>.{0,20}\/ month<\/span>/);
	assert.match(html, /1,500 credits every month/);
	assert.match(html, /500 credits · \$5/);
	assert.doesNotMatch(words(html), /\bcard\b|Stripe/i);
	// No subscription: no portal or cancel buttons.
	assert.doesNotMatch(html, /Manage subscription/);
});

test("crypto only: Upgrade and Buy by crypto, prepaid months, no card anywhere", () => {
	const base = me();
	const html = view(
		me({
			mode: "live",
			payments: { ...base.payments, crypto: { ...base.payments.crypto, enabled: true } },
		})
	);
	assert.doesNotMatch(html, /Payments aren/);
	assert.match(html, /aria-label="Upgrade: Plus"/);
	assert.match(html, /aria-label="Upgrade: Pro"/);
	assert.match(html, /aria-label="Buy: 500 credits"/);
	assert.match(html, /paid in crypto, 1, 3, or 12 months at a time/);
	assert.doesNotMatch(words(html), /\bcard\b|Stripe|Manage subscription/i);
});

test("live mode hides the notice; off mode says nothing is measured", () => {
	assert.doesNotMatch(view(me({ mode: "live" })), /billing-mode-notice/);
	assert.match(view(me({ mode: "off" })), /Billing is off/);
});

test("an exempt account shows the unlimited badge instead of an allowance bar", () => {
	const html = view(me({ exempt: true }));
	assert.match(html, /Unlimited/);
	assert.match(html, /usage is recorded but never charged/);
	assert.doesNotMatch(html, /role="progressbar"/);
});

test("a card subscriber sees renewal, the portal, cancel, and Downgrade", () => {
	const base = me();
	const html = view(
		me({
			mode: "live",
			plan: PLUS,
			account: {
				...base.account,
				plan_id: PLUS.id,
				plan_source: "stripe",
				has_subscription: true,
			},
			payments: { ...base.payments, card: true },
		})
	);
	assert.match(html, /Card subscription · renews on Oct 1, 2026/);
	assert.match(html, /Manage subscription/);
	assert.match(html, /Cancel subscription/);
	assert.match(html, /aria-label="Downgrade: Free"/);
	assert.match(html, /aria-label="Upgrade: Pro"/);
	assert.doesNotMatch(html, /Payments aren/);
});

test("a crypto-prepaid plan says until when", () => {
	const base = me();
	const html = view(
		me({
			plan: PLUS,
			account: {
				...base.account,
				plan_id: PLUS.id,
				plan_source: "crypto",
				paid_until: "2027-03-01T00:00:00Z",
			},
		})
	);
	assert.match(html, /Prepaid with crypto until Mar 1, 2027/);
});

test("the view renders in another locale from the same data", () => {
	const html = view(me(), "es");
	assert.match(html, /Plan actual/);
	assert.match(html, /Se está midiendo el uso/);
});

test("usage rows show the action label from prices and mark shadow rows", () => {
	const labels = new Map([["studio_quiz", "Quiz"]]);
	const html = render(
		createElement(UsageTable, {
			labels,
			data: {
				total: 1,
				by_action: [],
				items: [
					{
						id: 1,
						action: "studio_quiz",
						units: 1,
						credits: 5,
						mode: "shadow",
						status: "settled",
						denial_code: null,
						workspace_id: 1,
						notebook_id: 12,
						created_at: "2026-09-20T10:00:00Z",
					},
				],
			},
		})
	);
	assert.match(html, />Quiz</);
	assert.match(html, />Charged/);
	assert.match(html, /\(not charged\)/);
	assert.match(html, />#12</);
});

test("a paid card payment links its receipt; an open crypto one can be resumed", () => {
	const card = render(
		createElement(PaymentRow, {
			payment: {
				id: 1,
				provider: "stripe",
				kind: "pack",
				status: "paid",
				amount_cents: 500,
				currency: "usd",
				credits: 500,
				plan_id: null,
				months: null,
				receipt_url: "https://pay.stripe.com/receipts/x",
				created_at: "2026-09-20T10:00:00Z",
				completed_at: "2026-09-20T10:01:00Z",
				crypto: null,
			},
		})
	);
	assert.match(card, /500 credits · \$5/);
	assert.match(card, /href="https:\/\/pay.stripe.com\/receipts\/x" target="_blank"/);
	assert.match(card, />Paid</);

	const cryptoPayment = (status: string) => ({
		id: 2,
		provider: "btcpay",
		kind: "plan_months",
		status,
		amount_cents: 2700,
		currency: "usd",
		credits: 0,
		plan_id: 2,
		months: 3,
		receipt_url: null,
		created_at: "2026-09-20T10:00:00Z",
		completed_at: null,
		crypto: { payment_url: "https://pay.example.com/i/inv1" },
	});
	const pending = render(createElement(PaymentRow, { payment: cryptoPayment("pending") }));
	assert.match(pending, /3 months of a plan · \$27/);
	assert.match(pending, /Waiting for payment/);
	assert.match(pending, /<a href="https:\/\/pay.example.com\/i\/inv1"[^>]*>Continue payment<\/a>/);
	assert.doesNotMatch(pending, /finish on the payment page/);

	const partial = render(createElement(PaymentRow, { payment: cryptoPayment("partial") }));
	assert.match(partial, /Partly paid — finish on the payment page\./);
	assert.match(partial, />Continue payment</);

	const paid = render(createElement(PaymentRow, { payment: cryptoPayment("paid") }));
	assert.doesNotMatch(paid, /Continue payment/);
});
