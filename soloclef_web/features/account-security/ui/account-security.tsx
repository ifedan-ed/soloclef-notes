"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useSignInOptions } from "@/features/sign-in-options/hooks/use-sign-in-options";
import { ssoProviderName } from "@/features/sign-in-options/model/sign-in-options";
import { usePasswordState } from "../hooks/use-password-state";
import { DevicesCard } from "./devices-card";
import { PasswordCard } from "./password-card";
import { SignOutEverywhere } from "./sign-out-everywhere";

/**
 * Settings → Security: the account's password, where it is signed in, and
 * signing out everywhere. The same three things as the phone app's Password
 * and Signed-in devices screens, on the same routes.
 */
export function AccountSecurity() {
	const t = useTranslations("account_security");
	const passwordState = usePasswordState();
	// Only an account with no password needs the provider's name, and only
	// in a sentence, so it is read only for one and the page never waits for
	// it: until it arrives (or if it cannot be read) the sentence says
	// "single sign-on".
	const signInOptions = useSignInOptions({
		enabled: passwordState.data?.has_password === false,
	});
	const provider = ssoProviderName(signInOptions.data, t("sso_fallback_name"));

	return (
		<div className="space-y-10" data-testid="account-security">
			{passwordState.isError ? (
				<div className="flex flex-col items-start gap-3 rounded-lg border p-4">
					<p role="alert" className="text-sm text-destructive">
						{t("password_load_failed")}
					</p>
					<Button variant="outline" size="sm" onClick={() => void passwordState.refetch()}>
						{t("retry")}
					</Button>
				</div>
			) : passwordState.data ? (
				<PasswordCard hasPassword={passwordState.data.has_password} providerName={provider} />
			) : (
				<PasswordSkeleton />
			)}
			<DevicesCard />
			<SignOutEverywhere />
		</div>
	);
}

/** The password form's shape while the account is read: a heading, three fields, the button. */
function PasswordSkeleton() {
	return (
		<div className="space-y-4" aria-busy>
			<Skeleton className="h-5 w-32" />
			{["a", "b", "c"].map((key) => (
				<div key={key} className="space-y-2">
					<Skeleton className="h-4 w-28" />
					<Skeleton className="h-9 w-full" />
				</div>
			))}
		</div>
	);
}
