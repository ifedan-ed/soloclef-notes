"use client";

import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { ConnectorCard } from "@/components/assistant-ui/connector-popup/components/connector-card";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { EnumConnectorName } from "@/contracts/enums/connector";
import { useJmapAccounts } from "../hooks/use-jmap-accounts";
import { useJmapMailReturn } from "../hooks/use-jmap-mail-return";
import { useJmapOAuthAvailability } from "../hooks/use-jmap-oauth-availability";
import { useJmapOAuthConnect } from "../hooks/use-jmap-oauth-connect";
import { JmapAccountRow } from "./jmap-account-row";
import { JmapAppPasswordForm } from "./jmap-app-password-form";
import { JmapOAuthForm } from "./jmap-oauth-form";

type OpenForm = "oauth" | "app_password" | null;

/**
 * Stalwart (JMAP) in the connectors catalog, built like the Email (IMAP)
 * card: it expands inline into the account list and the ways to connect.
 * Stalwart's own sign-in comes first when the deployment offers it; the app
 * password form is the other way, never a silent stand-in for it.
 */
export function JmapMailConnectorCard() {
	const t = useTranslations("jmap_mail_connect");
	const { accounts, loading, refreshQuietly, addAccount, removeAccount, testAccount } =
		useJmapAccounts();
	const oauthConfigured = useJmapOAuthAvailability();
	const { start: startOAuth, starting: startingOAuth } = useJmapOAuthConnect();
	const [expanded, setExpanded] = useState(false);
	const [openForm, setOpenForm] = useState<OpenForm>(null);

	const showReturnedMailbox = useCallback(() => {
		setExpanded(true);
		void refreshQuietly();
	}, [refreshQuietly]);
	useJmapMailReturn(showReturnedMailbox);

	const toggle = () => setExpanded((prev) => !prev);

	return (
		<div className="flex flex-col gap-2" data-testid="jmap-mail-connector-card">
			<ConnectorCard
				id="jmap-mail-connector"
				title={t("title")}
				description={t("card_description")}
				connectorType={EnumConnectorName.EMAIL_IMAP_CONNECTOR}
				isConnected={accounts.length > 0}
				accountCount={accounts.length}
				statusText={
					accounts.length > 0 ? t("card_description_count", { count: accounts.length }) : undefined
				}
				onConnect={toggle}
				onManage={toggle}
			/>

			{expanded && (
				<div className="flex flex-col gap-3 rounded-lg border border-border/60 p-4">
					<p className="text-xs text-muted-foreground">{t("send_note")}</p>

					{loading ? (
						<div className="flex items-center justify-center py-4">
							<Spinner size="sm" />
						</div>
					) : accounts.length === 0 && openForm === null ? (
						<p className="text-sm text-muted-foreground">{t("no_accounts")}</p>
					) : (
						<div className="flex flex-col gap-2">
							{accounts.map((account) => (
								<JmapAccountRow
									key={account.id}
									account={account}
									onTest={testAccount}
									onRemove={removeAccount}
									onReconnect={oauthConfigured ? startOAuth : undefined}
								/>
							))}
						</div>
					)}

					{openForm === "oauth" ? (
						<JmapOAuthForm
							onStart={startOAuth}
							starting={startingOAuth}
							onCancel={() => setOpenForm(null)}
						/>
					) : openForm === "app_password" ? (
						<JmapAppPasswordForm
							onSave={addAccount}
							onAdded={() => setOpenForm(null)}
							onCancel={() => setOpenForm(null)}
						/>
					) : (
						<div className="flex flex-wrap items-center gap-2">
							{oauthConfigured && (
								<Button
									type="button"
									size="sm"
									className="w-fit"
									onClick={() => setOpenForm("oauth")}
									data-testid="jmap-connect-oauth"
								>
									{t("connect_oauth")}
								</Button>
							)}
							<Button
								type="button"
								variant="outline"
								size="sm"
								className="w-fit"
								onClick={() => setOpenForm("app_password")}
								data-testid="jmap-connect-app-password"
							>
								{t("connect_app_password")}
							</Button>
						</div>
					)}
				</div>
			)}
		</div>
	);
}
