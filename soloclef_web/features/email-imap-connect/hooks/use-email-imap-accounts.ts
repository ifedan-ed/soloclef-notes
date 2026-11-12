"use client";

import { useCallback, useEffect, useState } from "react";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";
import {
	type EmailImapAccount,
	emailImapApi,
	type NewEmailImapAccount,
} from "../api/email-imap-api";

/**
 * The first mailbox creates the Email (IMAP) connection and the last one's
 * removal leaves it empty; "Your connectors" reads connections from the
 * connectors list, so re-read it after either (as Nextcloud's hook does).
 */
function refreshConnectorsList() {
	void queryClient.invalidateQueries({ queryKey: cacheKeys.connectors.all() });
}

export function useEmailImapAccounts() {
	const [accounts, setAccounts] = useState<EmailImapAccount[]>([]);
	const [loading, setLoading] = useState(true);

	const refresh = useCallback(async () => {
		setLoading(true);
		try {
			const list = await emailImapApi.list();
			setAccounts(list);
		} finally {
			setLoading(false);
		}
	}, []);

	/** Re-read the list in place, without the loading state (no flash). */
	const refreshQuietly = useCallback(async () => {
		try {
			setAccounts(await emailImapApi.list());
		} catch {
			// The list on screen stays; the next open reads it again.
		}
	}, []);

	useEffect(() => {
		void refresh();
	}, [refresh]);

	const addAccount = useCallback(async (account: NewEmailImapAccount) => {
		const created = await emailImapApi.add(account);
		setAccounts((prev) => [...prev, created]);
		refreshConnectorsList();
		return created;
	}, []);

	const removeAccount = useCallback(async (accountId: string) => {
		await emailImapApi.remove(accountId);
		setAccounts((prev) => prev.filter((a) => a.id !== accountId));
		refreshConnectorsList();
	}, []);

	const testAccount = useCallback(
		async (accountId: string) => {
			const result = await emailImapApi.test(accountId);
			// A failed test may have just found a Microsoft mailbox that needs
			// reconnecting; re-read so its row offers Reconnect.
			if (result.status === "error") void refreshQuietly();
			return result;
		},
		[refreshQuietly]
	);

	return { accounts, loading, refresh, refreshQuietly, addAccount, removeAccount, testAccount };
}
