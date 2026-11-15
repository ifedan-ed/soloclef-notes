"use client";

import { AnimatePresence, motion } from "motion/react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Logo } from "@/components/Logo";
import { Button } from "@/components/ui/button";
import { getAuthErrorDetails, shouldRetry } from "@/lib/auth-errors";
import { setRedirectPath } from "@/lib/auth-utils";
import { SSO_SETTINGS } from "@/lib/sso-settings";
import { LocalLoginForm } from "./LocalLoginForm";
import { SsoLoginButton } from "./SsoLoginButton";

/**
 * Rendered in the site design: listed in `SITE_DESIGN_ROUTES` in
 * `components/site/site-shell.tsx`, so the palette, ruled column and
 * navigation all come from `app/(home)/home.css`. `SiteShell` skips the
 * footer here (`isAuthPage`), same as it always has for this route.
 */

/**
 * The single sign-on button's words, or null with single sign-on off. They
 * are the build's (NEXT_PUBLIC_OIDC_ENABLED and NEXT_PUBLIC_OIDC_BUTTON_LABEL,
 * which docker-compose.go.yml fills from the API's own SOLOCLEF_API_OIDC_ENABLED
 * and SOLOCLEF_WEB_SSO_BUTTON_LABEL), so the page's HTML already has the right
 * form in it: no request to wait for and nothing that moves once it is drawn.
 */
const ssoLabel = SSO_SETTINGS.enabled ? SSO_SETTINGS.buttonLabel : null;

function LoginContent() {
	const t = useTranslations("auth");
	const tCommon = useTranslations("common");
	const router = useRouter();
	const [urlError, setUrlError] = useState<{ title: string; message: string } | null>(null);

	useEffect(() => {
		// Read once, in the browser, from the address itself rather than with
		// useSearchParams: that hook makes a static export leave the whole page
		// out of its HTML until the script runs, and the notices below only
		// ever need the address the page was opened with.
		const searchParams = new URLSearchParams(window.location.search);
		// Check for various URL parameters that might indicate success or error states
		const registered = searchParams.get("registered");
		const error = searchParams.get("error");
		const message = searchParams.get("message");
		const logout = searchParams.get("logout");
		const returnUrl = searchParams.get("returnUrl");

		// Save returnUrl for client-side login flows that can redirect directly after success.
		if (returnUrl) {
			setRedirectPath(decodeURIComponent(returnUrl));
		}

		// Show registration success message
		if (registered === "true") {
			toast.success(t("register_success"), {
				description: t("login_subtitle"),
				duration: 5000,
			});
		}

		// Show logout confirmation
		if (logout === "true") {
			toast.success(tCommon("success"), {
				description: "You have been securely logged out",
				duration: 3000,
			});
		}

		// Show error messages from OAuth or other flows using auth-errors utility
		if (error) {
			// Use the auth-errors utility to get proper error details
			const errorDetails = getAuthErrorDetails(error);

			// If we have a custom message from URL params, use it as description
			const errorDescription = message ? decodeURIComponent(message) : errorDetails.description;

			// Set persistent error display
			setUrlError({
				title: errorDetails.title,
				message: errorDescription,
			});

			// Show toast with conditional retry action
			const toastOptions: {
				description: string;
				duration: number;
				action?: { label: string; onClick: () => void };
			} = {
				description: errorDescription,
				duration: 6000,
			};

			// Add retry action if the error is retryable
			if (shouldRetry(error)) {
				toastOptions.action = {
					label: "Retry",
					onClick: () => router.refresh(),
				};
			}

			toast.error(errorDetails.title, toastOptions);
		}

		// Show general messages
		if (message && !error && !registered && !logout) {
			toast.info("Notice", {
				description: decodeURIComponent(message),
				duration: 4000,
			});
		}
	}, [router, t, tCommon]);

	return (
		<section className="ss-home-hero ss-home-pad flex min-h-screen items-center justify-center">
			<div className="mx-auto flex w-full max-w-md flex-col items-center text-center">
				<Logo priority className="h-14 w-14 rounded-md transition-all md:h-16 md:w-16" />
				<h1 className="ss-home-h2 mt-6 mb-2">{t("sign_in")}</h1>
				<p className="ss-home-body">{t("login_subtitle")}</p>

				{/* URL Error Display */}
				<AnimatePresence>
					{urlError && (
						<motion.div
							initial={{ opacity: 0, y: -10, scale: 0.95 }}
							animate={{ opacity: 1, y: 0, scale: 1 }}
							exit={{ opacity: 0, y: -10, scale: 0.95 }}
							transition={{ duration: 0.3 }}
							className="mt-6 w-full rounded-md border border-destructive/30 bg-destructive/10 p-4 text-left text-destructive shadow-sm"
						>
							<div className="flex items-start gap-3">
								<svg
									xmlns="http://www.w3.org/2000/svg"
									width="18"
									height="18"
									viewBox="0 0 24 24"
									fill="none"
									stroke="currentColor"
									strokeWidth="2"
									strokeLinecap="round"
									strokeLinejoin="round"
									className="shrink-0 mt-0.5 text-destructive"
								>
									<title>Error Icon</title>
									<circle cx="12" cy="12" r="10" />
									<line x1="15" y1="9" x2="9" y2="15" />
									<line x1="9" y1="9" x2="15" y2="15" />
								</svg>
								<div className="flex-1 min-w-0">
									<p className="text-sm font-semibold mb-1">{urlError.title}</p>
									<p className="text-sm text-destructive/90">{urlError.message}</p>
								</div>
								<Button
									type="button"
									variant="ghost"
									size="icon"
									onClick={() => setUrlError(null)}
									className="size-6 shrink-0 text-destructive hover:bg-transparent hover:text-destructive/80"
									aria-label="Dismiss error"
								>
									<svg
										xmlns="http://www.w3.org/2000/svg"
										width="16"
										height="16"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2"
										strokeLinecap="round"
										strokeLinejoin="round"
									>
										<title>Close</title>
										<line x1="18" y1="6" x2="6" y2="18" />
										<line x1="6" y1="6" x2="18" y2="18" />
									</svg>
								</Button>
							</div>
						</motion.div>
					)}
				</AnimatePresence>

				{ssoLabel && (
					<div className="mt-8 w-full">
						<SsoLoginButton label={ssoLabel} />
						<div className="mt-6 flex items-center gap-3 text-sm text-muted-foreground">
							<span className="h-px flex-1 bg-border" />
							<span>{t("or_sign_in_with_password")}</span>
							<span className="h-px flex-1 bg-border" />
						</div>
					</div>
				)}

				<div className={`${ssoLabel ? "mt-6" : "mt-8"} w-full text-left`}>
					<LocalLoginForm />
				</div>
			</div>
		</section>
	);
}

export default function LoginPage() {
	return <LoginContent />;
}
