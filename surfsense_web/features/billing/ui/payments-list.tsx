"use client";

import { ExternalLink } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { Payment } from "../api/billing-schemas";
import { useBillingPayments } from "../hooks/use-billing-queries";
import { continuePaymentUrl } from "../lib/crypto-payment";
import { formatCredits, formatDateTime, formatMoney } from "../lib/format";
import { SectionHeading } from "./section-heading";

/** Payments and invoices, newest first, with receipts and a way back into an open crypto payment. */
export function PaymentsList() {
	const t = useTranslations("billing");
	const payments = useBillingPayments();
	const items = payments.data?.items ?? [];
	return (
		<section className="space-y-3" aria-labelledby="billing-payments-title">
			<div id="billing-payments-title">
				<SectionHeading title={t("payments.title")} />
			</div>
			{payments.isError ? (
				<p className="text-sm text-destructive">{t("errors.load_failed")}</p>
			) : payments.data && items.length === 0 ? (
				<p className="text-sm text-muted-foreground">{t("payments.empty")}</p>
			) : (
				<ul className="divide-y rounded-lg border">
					{items.map((payment) => (
						<PaymentRow key={payment.id} payment={payment} />
					))}
				</ul>
			)}
		</section>
	);
}

export function PaymentRow({ payment }: { payment: Payment }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const what =
		payment.kind === "pack"
			? t("payments.kind_pack", { credits: formatCredits(payment.credits, locale) })
			: payment.kind === "plan_months"
				? t("payments.kind_plan_months", { count: payment.months ?? 1 })
				: t("payments.kind_subscription");
	const via = payment.provider === "stripe" ? t("payments.via_card") : t("payments.via_crypto");
	const status = t.has(`payment_status.${payment.status}`)
		? t(`payment_status.${payment.status}`)
		: payment.status;
	const continueUrl = continuePaymentUrl(payment);
	return (
		<li className="flex flex-wrap items-center justify-between gap-2 p-3 text-sm">
			<div className="min-w-0">
				<p className="font-medium">
					{what} · {formatMoney(payment.amount_cents, payment.currency, locale)}
				</p>
				<p className="text-muted-foreground">
					{formatDateTime(payment.created_at, locale)} · {via}
				</p>
				{payment.status === "partial" ? (
					<p className="text-destructive">{t("payments.partial_hint")}</p>
				) : null}
			</div>
			<div className="flex items-center gap-2">
				<Badge variant={payment.status === "paid" ? "default" : "secondary"}>{status}</Badge>
				{payment.receipt_url ? (
					<a
						className="inline-flex items-center gap-1 text-primary underline-offset-4 hover:underline"
						href={payment.receipt_url}
						target="_blank"
						rel="noopener noreferrer"
					>
						{t("payments.receipt")}
						<ExternalLink className="size-3" aria-hidden />
					</a>
				) : null}
				{continueUrl ? (
					// BTCPay's hosted checkout, in this tab like the rest of the payment flow.
					<Button asChild size="sm" variant="outline">
						<a href={continueUrl}>{t("payments.continue")}</a>
					</Button>
				) : null}
			</div>
		</li>
	);
}
