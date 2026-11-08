"use client";

import { useTranslations } from "next-intl";
import { type FC, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { EmailImapAccount } from "../api/email-imap-api";

interface EmailImapAccountRowProps {
	account: EmailImapAccount;
	onTest: (
		accountId: string
	) => Promise<{ status: string; folder_count?: number | null; message?: string | null }>;
	onRemove: (accountId: string) => Promise<void>;
	/** Microsoft's sign-in again for this mailbox; absent when the server can't. */
	onReconnect?: (accountId: string) => Promise<void>;
}

/**
 * One connected mailbox row: host/username (or "Microsoft"), an inline test
 * button (Reconnect instead when Microsoft stopped accepting the mailbox),
 * and an inline-confirm remove.
 */
export const EmailImapAccountRow: FC<EmailImapAccountRowProps> = ({
	account,
	onTest,
	onRemove,
	onReconnect,
}) => {
	const t = useTranslations("email_imap_connect");
	const isMicrosoft = account.auth === "microsoft";
	const offerReconnect = isMicrosoft && account.needs_reconnect && onReconnect !== undefined;
	const [testing, setTesting] = useState(false);
	const [reconnecting, setReconnecting] = useState(false);
	const [confirming, setConfirming] = useState(false);
	const [removing, setRemoving] = useState(false);

	const handleTest = async () => {
		setTesting(true);
		try {
			const result = await onTest(account.id);
			if (result.status === "ok") {
				toast.success(t("test_success", { count: result.folder_count ?? 0 }));
			} else {
				toast.error(t("test_failed", { message: result.message ?? "" }));
			}
		} finally {
			setTesting(false);
		}
	};

	const handleReconnect = async () => {
		if (!onReconnect) return;
		setReconnecting(true);
		// On success the browser leaves for Microsoft; only a failure returns here.
		await onReconnect(account.id);
		setReconnecting(false);
	};

	const handleRemove = async () => {
		setRemoving(true);
		try {
			await onRemove(account.id);
			toast.success(t("account_removed_toast", { label: account.label }));
		} catch {
			toast.error(t("delete_failed"));
		} finally {
			setRemoving(false);
			setConfirming(false);
		}
	};

	return (
		<div className="flex flex-col gap-2 rounded-md border border-border/50 px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
			<div className="min-w-0">
				<p className="truncate text-sm font-medium">{account.label}</p>
				<p className="truncate text-xs text-muted-foreground">
					{isMicrosoft ? (
						<>
							{account.username} · {t("microsoft_account")}
						</>
					) : (
						<>
							{account.username} · {account.host}:{account.port}
							{account.use_tls ? " · TLS" : ""}
						</>
					)}
				</p>
				{isMicrosoft && account.needs_reconnect && (
					<p className="text-xs text-destructive">{t("needs_reconnect")}</p>
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
