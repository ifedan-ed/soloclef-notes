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
function refreshConnectorsList(workspaceId: number) {
	void queryClient.invalidateQueries({ queryKey: cacheKeys.connectors.all(String(workspaceId)) });
}

export function useEmailImapAccounts(workspaceId: number | null) {
	const [accounts, setAccounts] = useState<EmailImapAccount[]>([]);
	const [loading, setLoading] = useState(true);

	const refresh = useCallback(async () => {
		if (!workspaceId) return;
		setLoading(true);
		try {
			const list = await emailImapApi.list(workspaceId);
			setAccounts(list);
		} finally {
			setLoading(false);
		}
	}, [workspaceId]);

	/** Re-read the list in place, without the loading state (no flash). */
	const refreshQuietly = useCallback(async () => {
		if (!workspaceId) return;
		try {
			setAccounts(await emailImapApi.list(workspaceId));
		} catch {
			// The list on screen stays; the next open reads it again.
		}
	}, [workspaceId]);

	useEffect(() => {
		void refresh();
	}, [refresh]);

	const addAccount = useCallback(
		async (account: NewEmailImapAccount) => {
			if (!workspaceId) throw new Error("No workspace selected");
			const created = await emailImapApi.add(workspaceId, account);
			setAccounts((prev) => [...prev, created]);
			refreshConnectorsList(workspaceId);
			return created;
		},
		[workspaceId]
	);

	const removeAccount = useCallback(
		async (accountId: string) => {
			if (!workspaceId) return;
			await emailImapApi.remove(workspaceId, accountId);
			setAccounts((prev) => prev.filter((a) => a.id !== accountId));
			refreshConnectorsList(workspaceId);
		},
		[workspaceId]
	);

	const testAccount = useCallback(
		async (accountId: string) => {
			if (!workspaceId) throw new Error("No workspace selected");
			const result = await emailImapApi.test(workspaceId, accountId);
			// A failed test may have just found a Microsoft mailbox that needs
			// reconnecting; re-read so its row offers Reconnect.
			if (result.status === "error") void refreshQuietly();
			return result;
		},
		[workspaceId, refreshQuietly]
	);

	return { accounts, loading, refresh, refreshQuietly, addAccount, removeAccount, testAccount };
}
