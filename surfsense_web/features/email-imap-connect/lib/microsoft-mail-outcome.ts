/**
 * Coming back from Microsoft's sign-in: the backend sends the browser to the
 * page it started on with `?microsoft_mail=<outcome>` (codes in
 * `surfsense_backend/app/connectors/email_imap/microsoft/outcomes.py`).
 */
export const MICROSOFT_MAIL_PARAM = "microsoft_mail";

export type MicrosoftMailMessage = {
	tone: "success" | "error";
	/** Key in the `email_imap_connect` messages. */
	messageKey: string;
};

const ERROR_KEYS: Record<string, string> = {
	admin_approval: "microsoft_admin_approval",
	scope_missing: "microsoft_scope_missing",
	no_refresh_token: "microsoft_scope_missing",
	expired: "microsoft_expired",
	no_address: "microsoft_no_address",
};

/** What to tell the person; `null` when they cancelled (they know) or there is nothing. */
export function microsoftMailOutcome(outcome: string | null): MicrosoftMailMessage | null {
	if (!outcome || outcome === "denied") return null;
	if (outcome === "connected") return { tone: "success", messageKey: "microsoft_connected" };
	return { tone: "error", messageKey: ERROR_KEYS[outcome] ?? "microsoft_connect_failed" };
}

/** This page as a path to come back to, without a previous outcome. */
export function returnPathWithoutOutcome(location: { pathname: string; search: string }): string {
	const params = new URLSearchParams(location.search);
	params.delete(MICROSOFT_MAIL_PARAM);
	const query = params.toString();
	return `${location.pathname}${query ? `?${query}` : ""}`;
}

/**
 * The Outlook / Microsoft 365 hint under the account list: the button when
 * the server has Microsoft set up, "not supported yet" when it has not,
 * nothing while that is still unknown.
 */
export function outlookHintKey(
	configured: boolean | undefined
): "outlook_hint_microsoft" | "outlook_hint" | null {
	if (configured === undefined) return null;
	return configured ? "outlook_hint_microsoft" : "outlook_hint";
}
