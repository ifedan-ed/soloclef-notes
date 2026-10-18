"use client";

import { useState } from "react";
import { useRuntimeConfig } from "@/components/providers/runtime-config";
import { Button } from "@/components/ui/button";
import { getAndClearRedirectPath } from "@/lib/auth-utils";
import { buildSsoStartUrl } from "@/lib/sso-login";

/**
 * Sign in through the server's OpenID Connect provider (OIDC_DISCOVERY_URL)
 * instead of typing a password; OIDC_BUTTON_LABEL names the button.
 * A full-page navigation, not a fetch: the backend answers with a redirect
 * to the provider and sets the cookie that ties the callback to this browser.
 */
export function SsoLoginButton() {
	const { oidcButtonLabel } = useRuntimeConfig();
	const [isRedirecting, setIsRedirecting] = useState(false);

	const handleClick = () => {
		if (isRedirecting) return;
		setIsRedirecting(true);
		window.location.href = buildSsoStartUrl(getAndClearRedirectPath());
	};

	return (
		<Button
			type="button"
			variant="outline"
			className="w-full rounded-md py-5 text-base font-medium"
			disabled={isRedirecting}
			onClick={handleClick}
		>
			{oidcButtonLabel}
		</Button>
	);
}
