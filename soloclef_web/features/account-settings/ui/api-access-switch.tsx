"use client";

import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { useAccountSettings, useUpdateAccountSettings } from "../hooks/use-account-settings";

/**
 * The account's one access control. While it is off, no API key and no
 * signed-in agent can act for the account, even with a valid key: a key is
 * made on purpose, but an agent's token comes from the identity provider and
 * nothing the account did stands behind it. Off for a new account.
 */
export function ApiAccessSwitch() {
	const settings = useAccountSettings();
	const update = useUpdateAccountSettings();

	if (settings.isLoading) {
		return (
			<div className="flex items-center justify-between gap-4 rounded-lg border p-4">
				<div className="space-y-2">
					<Skeleton className="h-4 w-32" />
					<Skeleton className="h-3 w-64" />
				</div>
				<Skeleton className="h-6 w-11 rounded-full" />
			</div>
		);
	}

	if (settings.isError || !settings.data) {
		return (
			<div className="flex items-center justify-between gap-4 rounded-lg border p-4">
				<div className="space-y-1">
					<Label>API and agent access</Label>
					<p className="text-xs text-destructive">Could not load this setting.</p>
				</div>
				<Button variant="outline" size="sm" onClick={() => settings.refetch()}>
					Retry
				</Button>
			</div>
		);
	}

	const enabled = settings.data.api_access_enabled;
	const toggle = async (next: boolean) => {
		try {
			await update.mutateAsync({ api_access_enabled: next });
			toast.success(next ? "API and agent access is on" : "API and agent access is off");
		} catch (error) {
			toast.error(error instanceof Error ? error.message : "Could not change API access");
		}
	};

	return (
		<div
			className="flex flex-col gap-3 rounded-lg border p-4 sm:flex-row sm:items-center sm:justify-between"
			data-testid="api-access-switch"
		>
			<div className="space-y-1">
				<Label htmlFor="api-access-enabled">API and agent access</Label>
				<p className="text-sm text-muted-foreground">
					When this is on, your API keys and the AI agents you sign in (such as Claude or ChatGPT)
					can read and change everything in your account: your notebooks, documents, notes and
					chats. When it is off, they are refused, even with a valid key.
				</p>
			</div>
			<Switch
				id="api-access-enabled"
				checked={enabled}
				disabled={update.isPending}
				onCheckedChange={toggle}
			/>
		</div>
	);
}
