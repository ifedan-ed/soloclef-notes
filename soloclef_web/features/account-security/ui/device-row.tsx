"use client";

import { Globe, Smartphone } from "lucide-react";
import { useLocale, useTranslations } from "next-intl";
import { useState } from "react";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { deviceName, showsEnd } from "../lib/device-description";
import type { Device } from "../model/device";

/**
 * One place the account is signed in: what it is, when it signed in and was
 * last used, and Sign out, confirmed first. This browser's own row is marked,
 * and its Sign out signs this browser out (the account menu's Logout), as the
 * phone app's own row leads to its Sign out.
 */
export function DeviceRow({
	device,
	signingOut,
	onSignOut,
}: {
	device: Device;
	signingOut: boolean;
	onSignOut: (device: Device) => void;
}) {
	const t = useTranslations("account_security");
	const locale = useLocale();
	const [confirming, setConfirming] = useState(false);
	const when = (value: string) =>
		new Date(value).toLocaleString(locale, { dateStyle: "medium", timeStyle: "short" });
	const { label, unnamed } = deviceName(device);
	const name = label ?? t(unnamed);
	const Icon = device.kind === "app" ? Smartphone : Globe;
	const detail = [
		t("signed_in_at", { when: when(device.signed_in_at) }),
		t("last_used_at", { when: when(device.last_used_at) }),
		showsEnd(device) ? t("ends_at", { when: when(device.expires_at) }) : null,
	]
		.filter(Boolean)
		.join(" · ");

	return (
		<li
			className={
				device.current
					? "flex flex-col gap-3 rounded-lg border border-foreground/30 bg-accent/30 p-4 sm:flex-row sm:items-center"
					: "flex flex-col gap-3 rounded-lg border bg-accent/10 p-4 sm:flex-row sm:items-center"
			}
			data-testid="device-row"
			data-current={device.current || undefined}
		>
			<div className="flex min-w-0 flex-1 items-start gap-3">
				<Icon className="mt-0.5 size-5 shrink-0 text-muted-foreground" aria-hidden />
				<div className="min-w-0 flex-1 space-y-1">
					<div className="flex min-w-0 flex-wrap items-center gap-2">
						<h4 className="min-w-0 truncate text-sm font-semibold" title={name}>
							{name}
						</h4>
						{device.current ? (
							<Badge variant="secondary" className="shrink-0 text-[11px]">
								{t("this_browser")}
							</Badge>
						) : null}
					</div>
					<p className="text-xs text-pretty text-muted-foreground">{detail}</p>
				</div>
			</div>
			<AlertDialog open={confirming} onOpenChange={setConfirming}>
				<AlertDialogTrigger asChild>
					<Button
						variant="outline"
						size="sm"
						className="shrink-0 self-start sm:self-center"
						disabled={signingOut}
						aria-label={t("sign_out_device_named", { name })}
					>
						{t("sign_out_device")}
					</Button>
				</AlertDialogTrigger>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>
							{device.current ? t("sign_out_this_title") : t("sign_out_other_title")}
						</AlertDialogTitle>
						<AlertDialogDescription>
							{device.current ? t("sign_out_this_body") : t("sign_out_other_body", { name })}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
						<AlertDialogAction
							onClick={() => onSignOut(device)}
							className="bg-destructive text-white hover:bg-destructive/90"
						>
							{device.current ? t("sign_out_this_confirm") : t("sign_out_other_confirm")}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</li>
	);
}
