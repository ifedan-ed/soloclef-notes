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
import { Input } from "@/components/ui/input";
import { billingAdminApi } from "../../api/billing-admin-api";
import type { AdminOverview, AdminPrice, PriceBody } from "../../api/billing-admin-schemas";
import { useAdminMutation } from "../../hooks/use-billing-admin";
import { type ContentLocale, priceContent, priceLabels } from "../../lib/admin-forms";
import { formatCredits, parseNumber } from "../../lib/format";
import { LocalePicker } from "./locale-picker";

/** What each metered action costs, with its label in every language. */
export function AdminPricesTab({ overview }: { overview: AdminOverview }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const [editing, setEditing] = useState<AdminPrice | null>(null);
	const [confirmReset, setConfirmReset] = useState(false);
	const reset = useAdminMutation(() => billingAdminApi.resetPrices(), {
		success: t("admin.prices_reset_done"),
		onSuccess: () => setConfirmReset(false),
	});
	return (
		<div className="space-y-3">
			<div className="flex justify-end">
				<Button variant="outline" onClick={() => setConfirmReset(true)}>
					{t("admin.reset_prices")}
				</Button>
			</div>
			<ul className="divide-y rounded-lg border">
				{overview.prices.map((price) => (
					<li key={price.action} className="flex flex-wrap items-center justify-between gap-2 p-3">
						<div className="min-w-0">
							<p className="font-medium">{price.label}</p>
							<p className="text-sm text-muted-foreground">
								<span className="font-mono text-xs">{price.action}</span> ·{" "}
								{t("admin.credits_per_unit", {
									credits: formatCredits(price.credits, locale),
									unit: price.unit,
								})}
							</p>
						</div>
						<Button variant="outline" size="sm" onClick={() => setEditing(price)}>
							{t("admin.edit")}
						</Button>
					</li>
				))}
			</ul>

			<Dialog open={editing !== null} onOpenChange={(open) => !open && setEditing(null)}>
				<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
					{editing ? (
						<PriceEditor key={editing.action} price={editing} onClose={() => setEditing(null)} />
					) : null}
				</DialogContent>
			</Dialog>

			<Dialog open={confirmReset} onOpenChange={setConfirmReset}>
				<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
					<DialogHeader>
						<DialogTitle>{t("admin.reset_confirm_title")}</DialogTitle>
						<DialogDescription>{t("admin.reset_confirm_body")}</DialogDescription>
					</DialogHeader>
					<DialogFooter>
						<Button variant="ghost" onClick={() => setConfirmReset(false)}>
							{t("cancel")}
						</Button>
						<Button
							variant="destructive"
							disabled={reset.isPending}
							onClick={() => reset.mutate(undefined)}
						>
							{t("admin.reset_prices")}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</div>
	);
}

function PriceEditor({ price, onClose }: { price: AdminPrice; onClose: () => void }) {
	const t = useTranslations("billing");
	const [credits, setCredits] = useState(String(price.credits));
	const [unit, setUnit] = useState(price.unit);
	const [labels, setLabels] = useState(() => priceLabels(price.content));
	const [contentLocale, setContentLocale] = useState<ContentLocale>("en");
	const save = useAdminMutation(
		(body: PriceBody) => billingAdminApi.updatePrice(price.action, body),
		{ onSuccess: onClose }
	);
	const value = parseNumber(credits, -1);
	return (
		<>
			<DialogHeader>
				<DialogTitle>{t("admin.edit_price")}</DialogTitle>
				<DialogDescription className="font-mono text-xs">{price.action}</DialogDescription>
			</DialogHeader>
			<div className="grid grid-cols-2 gap-3">
				<div className="space-y-1">
					<label htmlFor="price-credits" className="text-sm font-medium">
						{t("admin.credits")}
					</label>
					<Input
						id="price-credits"
						inputMode="decimal"
						value={credits}
						onChange={(e) => setCredits(e.target.value)}
					/>
				</div>
				<div className="space-y-1">
					<label htmlFor="price-unit" className="text-sm font-medium">
						{t("admin.unit")}
					</label>
					<Input id="price-unit" value={unit} onChange={(e) => setUnit(e.target.value)} />
				</div>
			</div>
			<LocalePicker value={contentLocale} onChange={setContentLocale} />
			<div className="space-y-1">
				<label htmlFor="price-label" className="text-sm font-medium">
					{t("admin.label")}
				</label>
				<Input
					id="price-label"
					value={labels[contentLocale] ?? ""}
					onChange={(e) => setLabels((l) => ({ ...l, [contentLocale]: e.target.value }))}
				/>
			</div>
			<DialogFooter>
				<Button variant="ghost" onClick={onClose}>
					{t("cancel")}
				</Button>
				<Button
					disabled={value < 0 || !labels.en?.trim() || save.isPending}
					onClick={() =>
						save.mutate({
							credits: value,
							unit: unit.trim() || undefined,
							content: priceContent(labels),
						})
					}
				>
					{t("admin.save")}
				</Button>
			</DialogFooter>
		</>
	);
}
