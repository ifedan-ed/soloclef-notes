"use client";

import { useLocale, useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { SegmentedControl } from "@/components/ui/segmented-control";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import type { BillingMe, Pack, Plan } from "../api/billing-schemas";
import { useBillingActions } from "../hooks/use-billing-actions";
import {
	ANY_CRYPTO_METHOD,
	isCryptoBusy,
	methodMessageKey,
	payCurrencyFor,
} from "../lib/crypto-payment";
import { formatDate, formatMoney } from "../lib/format";
import {
	availableMethods,
	cryptoDiscount,
	cryptoPlanCents,
	isFreePlan,
	type PaymentMethod,
	periodEnd,
	planChoice,
} from "../lib/plans";
import { PaymentsUnavailable } from "./payments-unavailable";

export type PurchaseTarget = { kind: "plan"; plan: Plan } | { kind: "pack"; pack: Pack };

/** Choosing how to pay for a plan or pack: card checkout, a plan switch, or BTCPay crypto checkout. */
export function PurchaseDialog({
	target,
	me,
	onOpenChange,
}: {
	target: PurchaseTarget | null;
	me: BillingMe;
	onOpenChange: (open: boolean) => void;
}) {
	return (
		<Dialog open={target !== null} onOpenChange={onOpenChange}>
			<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
				{target ? (
					<PurchaseBody
						key={target.kind === "plan" ? `plan-${target.plan.id}` : `pack-${target.pack.id}`}
						target={target}
						me={me}
						onClose={() => onOpenChange(false)}
					/>
				) : null}
			</DialogContent>
		</Dialog>
	);
}

function PurchaseBody({
	target,
	me,
	onClose,
}: {
	target: PurchaseTarget;
	me: BillingMe;
	onClose: () => void;
}) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const actions = useBillingActions();
	const crypto = me.payments.crypto;
	const isPlan = target.kind === "plan";
	const changing = isPlan && planChoice(target.plan, me.plan, me.account) === "change";
	const methods = availableMethods(
		me.payments,
		isPlan ? { kind: "plan", hasSubscription: me.account.has_subscription } : { kind: "pack" }
	);
	const [method, setMethod] = useState<PaymentMethod>(methods[0] ?? "card");
	const [months, setMonths] = useState<number>(crypto.month_options[0] ?? 1);
	const [cryptoMethod, setCryptoMethod] = useState<string>(ANY_CRYPTO_METHOD);
	const busy =
		actions.checkout.isPending || actions.changePlan.isPending || actions.createCrypto.isPending;
	const showBusy = useDelayedFlag(busy);

	const name = isPlan ? target.plan.name : target.pack.name;
	const baseCents = isPlan ? target.plan.price_cents : target.pack.price_cents;
	const currencyCode = isPlan ? target.plan.currency : target.pack.currency;
	const money = (cents: number) => formatMoney(cents, currencyCode, locale);
	const cryptoCents =
		isPlan && method === "crypto"
			? cryptoPlanCents(baseCents, months, crypto.discounts)
			: baseCents;

	const payByCard = () => {
		if (changing && isPlan) {
			actions.changePlan.mutate(target.plan.id, { onSuccess: onClose });
			return;
		}
		actions.checkout.mutate(
			isPlan ? { kind: "plan", plan_id: target.plan.id } : { kind: "pack", pack_id: target.pack.id }
		);
	};
	const payByCrypto = () => {
		const pay_currency = payCurrencyFor(cryptoMethod);
		// On success the hook sends the buyer on to BTCPay's checkout page.
		actions.createCrypto.mutate(
			isPlan
				? { kind: "plan", plan_id: target.plan.id, months, pay_currency }
				: { kind: "pack", pack_id: target.pack.id, pay_currency }
		);
	};
	const cryptoBusy = method === "crypto" && isCryptoBusy(actions.createCrypto.error);

	const title = isPlan ? t("purchase.plan_title", { name }) : t("purchase.pack_title", { name });

	if (changing && isPlan) {
		const end = formatDate(periodEnd(me.account), locale);
		const toFree = isFreePlan(target.plan);
		return (
			<>
				<DialogHeader>
					<DialogTitle>{title}</DialogTitle>
					<DialogDescription>
						{toFree
							? t("purchase.change_to_free", { date: end || "—" })
							: t("purchase.change_prorated", { name })}
					</DialogDescription>
				</DialogHeader>
				{me.payments.card ? null : <PaymentsUnavailable />}
				<DialogFooter>
					<Button variant="ghost" onClick={onClose}>
						{t("cancel")}
					</Button>
					<Button onClick={payByCard} disabled={busy || !me.payments.card}>
						{showBusy ? <Spinner size="sm" /> : null}
						{toFree ? t("purchase.confirm_downgrade") : t("purchase.confirm_switch")}
					</Button>
				</DialogFooter>
			</>
		);
	}

	return (
		<>
			<DialogHeader>
				<DialogTitle>{title}</DialogTitle>
				<DialogDescription>
					{isPlan
						? methods.length === 1 && methods[0] === "crypto"
							? t("purchase.plan_price_prepaid", { price: money(baseCents) })
							: t("purchase.plan_price", { price: money(baseCents) })
						: t("purchase.pack_price", { price: money(baseCents) })}
				</DialogDescription>
			</DialogHeader>

			{methods.length === 0 ? (
				<PaymentsUnavailable />
			) : (
				<div className="space-y-4">
					{methods.length > 1 ? (
						<SegmentedControl
							ariaLabel={t("purchase.method")}
							value={method}
							onValueChange={(value) => setMethod(value as PaymentMethod)}
							options={methods.map((m) => ({ value: m, label: t(`purchase.method_${m}`) }))}
						/>
					) : null}

					{method === "card" ? (
						<p className="text-sm text-muted-foreground">{t("purchase.card_note")}</p>
					) : (
						<div className="grid gap-3 sm:grid-cols-2">
							{isPlan ? (
								<div className="space-y-1.5">
									<p className="text-sm font-medium" id="billing-months-label">
										{t("purchase.months")}
									</p>
									<Select value={String(months)} onValueChange={(v) => setMonths(Number(v))}>
										<SelectTrigger aria-labelledby="billing-months-label" className="w-full">
											<SelectValue />
										</SelectTrigger>
										<SelectContent>
											{crypto.month_options.map((m) => {
												const pct = cryptoDiscount(crypto.discounts, m);
												return (
													<SelectItem key={m} value={String(m)}>
														{pct > 0
															? t("purchase.months_discount", { count: m, percent: pct })
															: t("purchase.months_option", { count: m })}
													</SelectItem>
												);
											})}
										</SelectContent>
									</Select>
								</div>
							) : null}
							<div className="space-y-1.5">
								<p className="text-sm font-medium" id="billing-crypto-method-label">
									{t("purchase.crypto_method")}
								</p>
								<Select value={cryptoMethod} onValueChange={setCryptoMethod}>
									<SelectTrigger aria-labelledby="billing-crypto-method-label" className="w-full">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										<SelectItem value={ANY_CRYPTO_METHOD}>{t("crypto_method.any")}</SelectItem>
										{crypto.methods.map((id) => {
											const key = `crypto_method.${methodMessageKey(id)}`;
											return (
												<SelectItem key={id} value={id}>
													{t.has(key) ? t(key) : id}
												</SelectItem>
											);
										})}
									</SelectContent>
								</Select>
							</div>
							<p className="text-sm sm:col-span-2">
								{t("purchase.crypto_total", { price: money(cryptoCents) })}
							</p>
							<p className="text-sm text-muted-foreground sm:col-span-2">
								{t("purchase.crypto_note")}
							</p>
							{cryptoBusy ? (
								<p
									className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive sm:col-span-2"
									role="alert"
								>
									{me.payments.card ? t("purchase.crypto_busy") : t("purchase.crypto_busy_retry")}
								</p>
							) : null}
						</div>
					)}
				</div>
			)}

			<DialogFooter>
				<Button variant="ghost" onClick={onClose}>
					{t("cancel")}
				</Button>
				{methods.length > 0 ? (
					<Button onClick={method === "card" ? payByCard : payByCrypto} disabled={busy}>
						{showBusy ? <Spinner size="sm" /> : null}
						{method === "card" ? t("purchase.to_checkout") : t("purchase.to_crypto_checkout")}
					</Button>
				) : null}
			</DialogFooter>
		</>
	);
}
