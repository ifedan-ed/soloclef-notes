"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Switch } from "@/components/ui/switch";
import { billingAdminApi } from "../../api/billing-admin-api";
import type {
	AdminOverview,
	MethodStatus,
	SecretKey,
	SettingsUpdate,
} from "../../api/billing-admin-schemas";
import { type BillingMode, billingModes, cryptoMethods } from "../../api/billing-schemas";
import { useAdminMutation } from "../../hooks/use-billing-admin";
import {
	draftToValues,
	methodState,
	missingSettingKey,
	parseMonthOptions,
	type SettingsDraft,
	settingsToDraft,
	toggle,
} from "../../lib/admin-forms";
import { methodMessageKey } from "../../lib/crypto-payment";
import { CopyButton } from "../copy-button";
import { SecretField } from "./secret-field";

function Field({
	id,
	label,
	hint,
	children,
}: {
	id?: string;
	label: string;
	hint?: string;
	children: React.ReactNode;
}) {
	return (
		<div className="space-y-1.5">
			<label htmlFor={id} className="text-sm font-medium">
				{label}
			</label>
			{children}
			{hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
		</div>
	);
}

function SwitchRow({
	label,
	checked,
	onChange,
}: {
	label: string;
	checked: boolean;
	onChange: (value: boolean) => void;
}) {
	return (
		<div className="flex items-center justify-between gap-3">
			<span className="text-sm font-medium">{label}</span>
			<Switch checked={checked} onCheckedChange={onChange} aria-label={label} />
		</div>
	);
}

function WebhookUrl({ label, url }: { label: string; url: string }) {
	return (
		<div className="space-y-1">
			<p className="text-xs text-muted-foreground">{label}</p>
			<div className="flex items-center gap-2">
				<code className="min-w-0 flex-1 break-all rounded bg-muted px-2 py-1 font-mono text-xs">
					{url}
				</code>
				<CopyButton value={url} label={label} />
			</div>
		</div>
	);
}

/** Whether people are offered this method, and if not, what it still needs. */
function MethodStatusLine({ status, testId }: { status?: MethodStatus; testId: string }) {
	const t = useTranslations("billing");
	if (!status) return null;
	const state = methodState(status);
	return (
		<div className="space-y-1" data-testid={testId} data-state={state}>
			<Badge variant={state === "ready" ? "default" : "outline"}>
				{t(`admin.method_status.${state}`)}
			</Badge>
			{state === "ready" ? null : (
				<p className="text-xs text-muted-foreground">
					{t("admin.method_hidden")}{" "}
					{status.missing.length > 0
						? t("admin.method_missing", {
								items: status.missing.map((key) => t(missingSettingKey(key))).join(", "),
							})
						: t("admin.method_needs_switch")}
				</p>
			)}
		</div>
	);
}

/** Mode, payment providers and their keys, and the general billing numbers. */
export function AdminSettingsTab({ overview }: { overview: AdminOverview }) {
	const t = useTranslations("billing");
	const settings = overview.settings;
	const [draft, setDraft] = useState<SettingsDraft>(() => settingsToDraft(settings.values));
	const [secrets, setSecrets] = useState<Partial<Record<SecretKey, string>>>({});
	const [pendingMode, setPendingMode] = useState<BillingMode | null>(null);
	const save = useAdminMutation((body: SettingsUpdate) => billingAdminApi.saveSettings(body), {
		onSuccess: () => setSecrets({}),
	});
	const set = <K extends keyof SettingsDraft>(key: K, value: SettingsDraft[K]) =>
		setDraft((d) => ({ ...d, [key]: value }));
	const months = parseMonthOptions(draft.crypto_month_options);
	const secretStatus = (key: SecretKey) => settings.secrets[key];
	const secretProps = (key: SecretKey) => ({
		id: `billing-secret-${key}`,
		label: t(`admin.secrets.${key}`),
		status: secretStatus(key),
		value: secrets[key] ?? "",
		onChange: (value: string) => setSecrets((s) => ({ ...s, [key]: value })),
		onClear: () => save.mutate({ secrets: { [key]: "" } }),
		clearing: save.isPending,
	});

	const chooseMode = (mode: BillingMode) => {
		if (mode === settings.values.mode) return;
		if (mode === "live") setPendingMode(mode);
		else save.mutate({ values: { mode } });
	};

	const submit = () => {
		const filled = Object.fromEntries(
			Object.entries(secrets).filter(([, value]) => (value ?? "").trim() !== "")
		) as Partial<Record<SecretKey, string>>;
		save.mutate({ values: draftToValues(draft), secrets: filled });
	};

	return (
		<div className="space-y-8">
			<section className="space-y-3">
				<h3 className="text-sm font-semibold">{t("admin.mode_title")}</h3>
				<SegmentedControl
					ariaLabel={t("admin.mode_title")}
					value={settings.values.mode}
					onValueChange={(value) => chooseMode(value as BillingMode)}
					options={billingModes.map((mode) => ({ value: mode, label: t(`admin.modes.${mode}`) }))}
				/>
				<p className="text-xs text-muted-foreground">
					{t(`admin.mode_help.${settings.values.mode}`)}
				</p>
			</section>

			<section className="space-y-4 rounded-lg border p-4">
				<div className="flex flex-wrap items-center justify-between gap-2">
					<h3 className="text-sm font-semibold">{t("admin.stripe_title")}</h3>
					{settings.stripe_mode ? (
						<Badge variant={settings.stripe_mode === "live" ? "destructive" : "secondary"}>
							{t(`admin.stripe_mode.${settings.stripe_mode}`)}
						</Badge>
					) : null}
				</div>
				<MethodStatusLine status={settings.payments?.card} testId="billing-admin-card-status" />
				<SwitchRow
					label={t("admin.stripe_enabled")}
					checked={draft.stripe_enabled}
					onChange={(v) => set("stripe_enabled", v)}
				/>
				<Field id="billing-stripe-pk" label={t("admin.stripe_publishable_key")}>
					<Input
						id="billing-stripe-pk"
						value={draft.stripe_publishable_key}
						onChange={(e) => set("stripe_publishable_key", e.target.value)}
						autoComplete="off"
					/>
				</Field>
				<SecretField {...secretProps("stripe_secret_key")} />
				<SecretField {...secretProps("stripe_webhook_secret")} />
				<WebhookUrl label={t("admin.webhook_stripe")} url={overview.webhooks.stripe} />
			</section>

			<section className="space-y-4 rounded-lg border p-4">
				<h3 className="text-sm font-semibold">{t("admin.crypto_title")}</h3>
				<MethodStatusLine status={settings.payments?.crypto} testId="billing-admin-crypto-status" />
				<SwitchRow
					label={t("admin.crypto_enabled")}
					checked={draft.crypto_enabled}
					onChange={(v) => set("crypto_enabled", v)}
				/>
				<Field id="billing-btcpay-url" label={t("admin.btcpay_url")}>
					<Input
						id="billing-btcpay-url"
						type="url"
						inputMode="url"
						value={draft.btcpay_url}
						onChange={(e) => set("btcpay_url", e.target.value)}
						autoComplete="off"
						placeholder="https://btcpay.example.com"
					/>
				</Field>
				<Field id="billing-btcpay-store" label={t("admin.btcpay_store_id")}>
					<Input
						id="billing-btcpay-store"
						value={draft.btcpay_store_id}
						onChange={(e) => set("btcpay_store_id", e.target.value)}
						autoComplete="off"
					/>
				</Field>
				<SecretField {...secretProps("btcpay_api_key")} />
				<SecretField {...secretProps("btcpay_webhook_secret")} />
				<fieldset className="space-y-2">
					<legend className="text-sm font-medium">{t("admin.crypto_payment_methods")}</legend>
					{[
						...cryptoMethods,
						...draft.crypto_payment_methods.filter(
							(id) => !(cryptoMethods as readonly string[]).includes(id)
						),
					].map((id) => (
						<div key={id} className="flex items-center gap-2 text-sm">
							<Checkbox
								id={`billing-method-${id}`}
								checked={draft.crypto_payment_methods.includes(id)}
								onCheckedChange={(on) =>
									set(
										"crypto_payment_methods",
										toggle(draft.crypto_payment_methods, id, on === true)
									)
								}
							/>
							<label htmlFor={`billing-method-${id}`}>
								{t.has(`crypto_method.${methodMessageKey(id)}`)
									? t(`crypto_method.${methodMessageKey(id)}`)
									: id}
							</label>
						</div>
					))}
				</fieldset>
				<Field
					id="billing-months"
					label={t("admin.month_options")}
					hint={t("admin.month_options_hint")}
				>
					<Input
						id="billing-months"
						value={draft.crypto_month_options}
						onChange={(e) => set("crypto_month_options", e.target.value)}
						inputMode="numeric"
					/>
				</Field>
				{months.length > 0 ? (
					<fieldset className="space-y-2">
						<legend className="text-sm font-medium">{t("admin.discounts")}</legend>
						<div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
							{months.map((m) => (
								<div key={m} className="space-y-1">
									<label
										htmlFor={`billing-discount-${m}`}
										className="text-xs text-muted-foreground"
									>
										{t("purchase.months_option", { count: m })}
									</label>
									<Input
										id={`billing-discount-${m}`}
										inputMode="decimal"
										value={draft.crypto_discounts[String(m)] ?? ""}
										placeholder="0"
										onChange={(e) =>
											set("crypto_discounts", {
												...draft.crypto_discounts,
												[String(m)]: e.target.value,
											})
										}
									/>
								</div>
							))}
						</div>
						<p className="text-xs text-muted-foreground">{t("admin.discounts_hint")}</p>
					</fieldset>
				) : null}
				<WebhookUrl label={t("admin.webhook_crypto")} url={overview.webhooks.crypto} />
				<p className="text-xs text-muted-foreground">{t("admin.btcpay_webhook_hint")}</p>
			</section>

			<section className="space-y-4 rounded-lg border p-4">
				<h3 className="text-sm font-semibold">{t("admin.general_title")}</h3>
				<div className="grid gap-4 sm:grid-cols-3">
					<Field id="billing-signup-bonus" label={t("admin.signup_bonus")}>
						<Input
							id="billing-signup-bonus"
							inputMode="decimal"
							value={draft.signup_bonus_credits}
							onChange={(e) => set("signup_bonus_credits", e.target.value)}
						/>
					</Field>
					<Field id="billing-low-balance" label={t("admin.low_balance")}>
						<Input
							id="billing-low-balance"
							inputMode="decimal"
							value={draft.low_balance_credits}
							onChange={(e) => set("low_balance_credits", e.target.value)}
						/>
					</Field>
					<Field id="billing-stale-hours" label={t("admin.stale_hold_hours")}>
						<Input
							id="billing-stale-hours"
							inputMode="numeric"
							value={draft.stale_hold_hours}
							onChange={(e) => set("stale_hold_hours", e.target.value)}
						/>
					</Field>
				</div>
				<Field id="billing-support-email" label={t("admin.support_email")}>
					<Input
						id="billing-support-email"
						type="email"
						autoComplete="off"
						value={draft.support_email}
						onChange={(e) => set("support_email", e.target.value)}
					/>
				</Field>
				<p className="text-xs text-muted-foreground">{t("admin.support_email_hint")}</p>
			</section>

			<div className="flex justify-end">
				<Button onClick={submit} disabled={save.isPending}>
					{t("admin.save_settings")}
				</Button>
			</div>

			<Dialog open={pendingMode !== null} onOpenChange={(open) => !open && setPendingMode(null)}>
				<DialogContent className="w-[calc(100vw-2rem)] max-w-md">
					<DialogHeader>
						<DialogTitle>{t("admin.live_confirm_title")}</DialogTitle>
						<DialogDescription>{t("admin.live_confirm_body")}</DialogDescription>
					</DialogHeader>
					<DialogFooter>
						<Button variant="ghost" onClick={() => setPendingMode(null)}>
							{t("cancel")}
						</Button>
						<Button
							variant="destructive"
							disabled={save.isPending}
							onClick={() =>
								save.mutate({ values: { mode: "live" } }, { onSuccess: () => setPendingMode(null) })
							}
						>
							{t("admin.live_confirm")}
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</div>
	);
}
