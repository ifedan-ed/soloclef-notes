"use client";

import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { ConnectorCard } from "@/components/assistant-ui/connector-popup/components/connector-card";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { EnumConnectorName } from "@/contracts/enums/connector";
import { useEmailImapAccounts } from "../hooks/use-email-imap-accounts";
import { useMicrosoftMailAvailability } from "../hooks/use-microsoft-mail-availability";
import { useMicrosoftMailConnect } from "../hooks/use-microsoft-mail-connect";
import { useMicrosoftMailReturn } from "../hooks/use-microsoft-mail-return";
import { outlookHintKey } from "../lib/microsoft-mail-outcome";
import { EmailImapAccountRow } from "./email-imap-account-row";
import { EmailImapAddAccountForm } from "./email-imap-add-account-form";

/**
 * Email (IMAP) in the connectors catalog: a card like Nextcloud/Google Drive,
 * but expanding inline (no dialog) into the account list + add-account form,
 * since this connector supports multiple accounts per user. Microsoft
 * mailboxes connect through Microsoft's sign-in instead of the form, when the
 * server has the Microsoft app set up.
 */
export function EmailImapConnectorCard({ workspaceId }: { workspaceId: number }) {
	const t = useTranslations("email_imap_connect");
	const { accounts, loading, refreshQuietly, addAccount, removeAccount, testAccount } =
		useEmailImapAccounts(workspaceId);
	const microsoftConfigured = useMicrosoftMailAvailability(workspaceId);
	const { start: connectMicrosoft, starting: connectingMicrosoft } =
		useMicrosoftMailConnect(workspaceId);
	const [expanded, setExpanded] = useState(false);
	const [showAddForm, setShowAddForm] = useState(false);
	const hintKey = outlookHintKey(microsoftConfigured);

	const showReturnedMailbox = useCallback(() => {
		setExpanded(true);
		void refreshQuietly();
	}, [refreshQuietly]);
	useMicrosoftMailReturn(showReturnedMailbox);

	const toggle = () => setExpanded((prev) => !prev);

	return (
		<div className="flex flex-col gap-2" data-testid="email-imap-connector-card">
			<ConnectorCard
				id="email-imap-connector"
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
					<p className="text-xs text-muted-foreground">{t("readonly_note")}</p>

					{loading ? (
						<div className="flex items-center justify-center py-4">
							<Spinner size="sm" />
						</div>
					) : accounts.length === 0 && !showAddForm ? (
						<p className="text-sm text-muted-foreground">{t("no_accounts")}</p>
					) : (
						<div className="flex flex-col gap-2">
							{accounts.map((account) => (
								<EmailImapAccountRow
									key={account.id}
									account={account}
									onTest={testAccount}
									onRemove={removeAccount}
									onReconnect={microsoftConfigured ? connectMicrosoft : undefined}
								/>
							))}
						</div>
					)}

					{showAddForm ? (
						<EmailImapAddAccountForm
							workspaceId={workspaceId}
							onSave={addAccount}
							onAdded={() => setShowAddForm(false)}
							onCancel={() => setShowAddForm(false)}
						/>
					) : (
						<div className="flex flex-wrap items-center gap-2">
							<Button
								type="button"
								variant="outline"
								size="sm"
								className="w-fit"
								onClick={() => setShowAddForm(true)}
							>
								{t("add_account")}
							</Button>
							{microsoftConfigured && (
								<Button
									type="button"
									variant="outline"
									size="sm"
									className="relative w-fit"
									onClick={() => void connectMicrosoft()}
									disabled={connectingMicrosoft}
									data-testid="email-imap-connect-microsoft"
								>
									<span className={connectingMicrosoft ? "opacity-0" : ""}>
										{t("connect_microsoft")}
									</span>
									{connectingMicrosoft && <Spinner size="xs" className="absolute" />}
								</Button>
							)}
						</div>
					)}

					<div className="flex flex-col gap-1 border-t border-border/40 pt-3 text-xs text-muted-foreground">
						<p>{t("gmail_hint")}</p>
						{hintKey && <p>{t(hintKey)}</p>}
					</div>
				</div>
			)}
		</div>
	);
}
