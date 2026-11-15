"use client";

import { useCallback, useEffect, useState } from "react";
import {
	type JmapAccount,
	type JmapRemoval,
	jmapMailApi,
	type NewJmapAccount,
} from "../api/jmap-mail-api";

/**
 * The account's Stalwart mailboxes and the four things the card does to them.
 * The list is held here, not in a query cache: only this card reads it, and
 * each change answers with what it changed, so the list is updated in place
 * without a second request.
 */
export function useJmapAccounts() {
	const [accounts, setAccounts] = useState<JmapAccount[]>([]);
	const [loading, setLoading] = useState(true);

	const refresh = useCallback(async () => {
		setLoading(true);
		try {
			setAccounts(await jmapMailApi.list());
		} catch {
			// The card shows "none connected"; opening it again asks again.
		} finally {
			setLoading(false);
		}
	}, []);

	/** Re-read the list in place, without the loading state (no flash). */
	const refreshQuietly = useCallback(async () => {
		try {
			setAccounts(await jmapMailApi.list());
		} catch {
			// The list on screen stays; the next open reads it again.
		}
	}, []);

	useEffect(() => {
		void refresh();
	}, [refresh]);

	const addAccount = useCallback(async (account: NewJmapAccount) => {
		const created = await jmapMailApi.add(account);
		// Adding the same server and username again replaces that mailbox's
		// sign-in on the server, so it replaces the row here too.
		setAccounts((prev) => [...prev.filter((a) => a.id !== created.id), created]);
		return created;
	}, []);

	const removeAccount = useCallback(async (accountId: string): Promise<JmapRemoval> => {
		const removal = await jmapMailApi.remove(accountId);
		setAccounts((prev) => prev.filter((a) => a.id !== accountId));
		return removal;
	}, []);

	const testAccount = useCallback(
		async (accountId: string) => {
			const result = await jmapMailApi.test(accountId);
			// A failed test may have just found a sign-in Stalwart no longer
			// accepts; re-read so the row offers Reconnect.
			if (result.status === "error") void refreshQuietly();
			return result;
		},
		[refreshQuietly]
	);

	return { accounts, loading, refreshQuietly, addAccount, removeAccount, testAccount };
}
