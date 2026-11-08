"use client";

import { ExternalLink } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { type LoginFlowState, loginFlowMessageKey } from "../lib/login-flow";

/**
 * The middle of a Connect: a plain link that opens Nextcloud's sign-in in a
 * new tab, while this page waits for the backend to report the connection.
 * Also says why an attempt ended, with a way to try again.
 */
export function NextcloudLoginPanel({
	state,
	server,
	onCancel,
	onRetry,
}: {
	state: LoginFlowState;
	server: string;
	onCancel: () => void;
	onRetry: () => void;
}) {
	const t = useTranslations("nextcloud_connect");
	if (state.phase === "waiting") {
		return (
			<div
				className="flex flex-col gap-3 rounded-xl border border-border bg-slate-400/5 p-4 text-sm dark:bg-white/5"
				data-testid="nextcloud-login-waiting"
			>
				<div className="flex flex-wrap items-center gap-2">
					<Button asChild size="sm">
						<a href={state.loginUrl} target="_blank" rel="noopener noreferrer">
							{t("open_login", { server })}
							<ExternalLink data-icon="inline-end" />
						</a>
					</Button>
					<Button variant="ghost" size="sm" onClick={onCancel}>
						{t("cancel")}
					</Button>
				</div>
				<p className="flex items-center gap-2 text-xs text-muted-foreground" aria-live="polite">
					<Spinner size="xs" />
					{t("waiting")}
				</p>
			</div>
		);
	}
	const messageKey = loginFlowMessageKey(state);
	if (!messageKey) return null;
	return (
		<div
			className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-border p-3 text-sm"
			role="alert"
		>
			<span className="text-muted-foreground">{t(messageKey)}</span>
			<Button variant="outline" size="sm" onClick={onRetry}>
				{t("try_again")}
			</Button>
		</div>
	);
}
