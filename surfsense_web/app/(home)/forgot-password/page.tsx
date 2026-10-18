"use client";

import { useTranslations } from "next-intl";
import { Logo } from "@/components/Logo";
import { ForgotPasswordForm } from "@/features/password-reset/ui/forgot-password-form";

/** "Forgot password?" from the sign-in page: ask for a reset link by email. */
export default function ForgotPasswordPage() {
	const t = useTranslations("auth");
	return (
		<section className="ss-home-hero ss-home-pad flex min-h-screen items-center justify-center">
			<div className="mx-auto flex w-full max-w-md flex-col items-center text-center">
				<Logo priority className="h-14 w-14 rounded-md md:h-16 md:w-16" />
				<h1 className="ss-home-h2 mt-6 mb-2">{t("forgot_title")}</h1>
				<p className="ss-home-body">{t("forgot_subtitle")}</p>
				<div className="mt-8 w-full text-left">
					<ForgotPasswordForm />
				</div>
			</div>
		</section>
	);
}
