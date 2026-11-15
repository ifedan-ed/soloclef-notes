"use client";

import { useTranslations } from "next-intl";
import { type FC, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { JmapAccount, JmapRemoval, JmapTestResult } from "../api/jmap-mail-api";
import { serverHost } from "../lib/jmap-mail-outcome";

interface JmapAccountRowProps {
	account: JmapAccount;
	onTest: (accountId: string) => Promise<JmapTestResult>;
	onRemove: (accountId: string) => Promise<JmapRemoval>;
	/** Stalwart's sign-in again, for this server; absent when the deployment can't offer it. */
	onReconnect?: (serverUrl: string) => Promise<void>;
}

/**
 * One connected Stalwart mailbox: who and where, how it signs in, whether the
 * assistant may draft mail to send from it, a test (or Reconnect when Stalwart
 * stopped accepting the sign-in) and a remove that asks first.
 */
export const JmapAccountRow: FC<JmapAccountRowProps> = ({
	account,
	onTest,
	onRemove,
	onReconnect,
}) => {
	const t = useTranslations("jmap_mail_connect");
	const isOAuth = account.auth === "oauth";
	// Only a sign-in can be renewed on Stalwart's page; an app password that
	// stopped working is replaced by adding the mailbox again with a new one.
	const offerReconnect = isOAuth && account.needs_reconnect && onReconnect !== undefined;
	const [testing, setTesting] = useState(false);
	const [reconnecting, setReconnecting] = useState(false);
	const [confirming, setConfirming] = useState(false);
	const [removing, setRemoving] = useState(false);

	const handleTest = async () => {
		setTesting(true);
		try {
			const result = await onTest(account.id);
			if (result.status === "ok") {
				toast.success(t("test_success", { count: result.mailbox_count ?? 0 }));
			} else {
				toast.error(t("test_failed", { message: result.message ?? "" }));
			}
		} catch {
			toast.error(t("test_failed", { message: "" }));
		} finally {
			setTesting(false);
		}
	};

	const handleReconnect = async () => {
		if (!onReconnect) return;
		setReconnecting(true);
		// On success the browser leaves for Stalwart; only a failure returns here.
		await onReconnect(account.server_url);
		setReconnecting(false);
	};

	const handleRemove = async () => {
		setRemoving(true);
		try {
			const { revoked } = await onRemove(account.id);
			// What is left at Stalwart decides the message: a removal that ended
			// nothing there must say where the person ends it themselves.
			if (revoked) toast.success(t("removed_revoked", { label: account.label }));
			else if (isOAuth) toast.warning(t("removed_oauth_not_revoked", { label: account.label }));
			else toast.warning(t("removed_app_password", { label: account.label }));
		} catch {
			toast.error(t("delete_failed"));
			setRemoving(false);
			setConfirming(false);
		}
	};

	return (
		// Stacked, not side by side: the card sits in half of the catalog's
		// grid, where buttons beside the text squeeze it to a word a line.
		<div
			className="flex flex-col gap-2 rounded-md border border-border/50 px-3 py-2"
			data-testid="jmap-account-row"
		>
			<div className="min-w-0 space-y-0.5">
				<p className="truncate text-sm font-medium" title={account.label}>
					{account.label}
				</p>
				<p className="truncate text-xs text-muted-foreground" title={account.username}>
					{account.username}
				</p>
				{/* How it signs in first: a long host name is what gets cut, not that. */}
				<p className="truncate text-xs text-muted-foreground" title={account.server_url}>
					{isOAuth ? t("auth_oauth") : t("auth_app_password")} · {serverHost(account.server_url)}
				</p>
				<p className="text-xs text-muted-foreground">
					{account.can_send ? t("can_send_note") : t("read_only_note")}
				</p>
				{account.needs_reconnect && (
					<p className="text-xs text-destructive">
						{isOAuth ? t("needs_reconnect_oauth") : t("needs_reconnect_app_password")}
					</p>
				)}
			</div>
			{confirming ? (
				<div className="flex flex-wrap items-center gap-2 text-xs">
					<span className="text-muted-foreground">
						{t("delete_confirm", { label: account.label })}
					</span>
					<Button
						type="button"
						variant="destructive"
						size="sm"
						onClick={handleRemove}
						disabled={removing}
						className="relative"
					>
						<span className={removing ? "opacity-0" : ""}>{t("delete_account")}</span>
						{removing && <Spinner size="xs" className="absolute" />}
					</Button>
					<Button
						type="button"
						variant="ghost"
						size="sm"
						onClick={() => setConfirming(false)}
						disabled={removing}
					>
						{t("cancel")}
					</Button>
				</div>
			) : (
				<div className="flex shrink-0 items-center gap-2">
					{offerReconnect ? (
						<Button
							type="button"
							size="sm"
							onClick={handleReconnect}
							disabled={reconnecting}
							className="relative"
						>
							<span className={reconnecting ? "opacity-0" : ""}>{t("reconnect")}</span>
							{reconnecting && <Spinner size="xs" className="absolute" />}
						</Button>
					) : (
						<Button
							type="button"
							variant="outline"
							size="sm"
							onClick={handleTest}
							disabled={testing}
							className="relative"
						>
							<span className={testing ? "opacity-0" : ""}>{t("test_connection")}</span>
							{testing && <Spinner size="xs" className="absolute" />}
						</Button>
					)}
					<Button type="button" variant="ghost" size="sm" onClick={() => setConfirming(true)}>
						{t("delete_account")}
					</Button>
				</div>
			)}
		</div>
	);
};
