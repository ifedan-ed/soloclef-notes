"use client";

import { useTranslations } from "next-intl";
import { Logo } from "@/components/Logo";
import { ForgotPasswordForm } from "@/features/password-reset/ui/forgot-password-form";
import { useSignInOptions } from "@/features/sign-in-options/hooks/use-sign-in-options";

/** "Forgot password?" from the sign-in page: ask for a reset link by email. */
export default function ForgotPasswordPage() {
	const t = useTranslations("auth");
	// On a server that sends no mail the form says so; "we'll send you a
	// link" above it would contradict it.
	const mailsLinks = useSignInOptions().data?.forgot_password !== false;
	return (
		<section className="ss-home-hero ss-home-pad ss-home-fill flex items-center justify-center">
			<div className="mx-auto flex w-full max-w-md flex-col items-center text-center">
				<Logo priority className="h-14 w-14 rounded-md md:h-16 md:w-16" />
				<h1 className="ss-home-h2 mt-6 mb-2">{t("forgot_title")}</h1>
				{mailsLinks ? <p className="ss-home-body">{t("forgot_subtitle")}</p> : null}
				<div className="mt-8 w-full text-left">
					<ForgotPasswordForm />
				</div>
			</div>
		</section>
	);
}
