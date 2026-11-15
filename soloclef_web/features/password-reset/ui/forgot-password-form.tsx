"use client";

import Link from "next/link";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { useSignInOptions } from "@/features/sign-in-options/hooks/use-sign-in-options";
import { requestPasswordReset } from "../api/password-reset-api";
import {
	classifyResetError,
	RESET_LINK_LIFETIME_HOURS,
	type ResetProblem,
} from "../lib/reset-errors";
import { AuthNotice, authInputClass } from "./auth-notice";
import { SubmitButton } from "./submit-button";

/**
 * Ask for a reset link. The confirmation is the same whether or not the
 * address has an account, so the form cannot be used to find out.
 *
 * A server that sends no mail cannot send a link. The sign-in options say
 * so before anything is typed, and the page then says it plainly instead of
 * offering a form whose every answer would be an error. While the options
 * are still on their way, or if they cannot be read, the form is shown: the
 * server's own refusal (PASSWORD_RESET_UNAVAILABLE) gives the same words.
 */
export function ForgotPasswordForm() {
	const t = useTranslations("auth");
	const options = useSignInOptions();
	const [email, setEmail] = useState("");
	const [pending, setPending] = useState(false);
	const [sentTo, setSentTo] = useState<string | null>(null);
	const [problem, setProblem] = useState<ResetProblem | null>(null);

	const submit = async (event: React.FormEvent) => {
		event.preventDefault();
		setProblem(null);
		setPending(true);
		try {
			await requestPasswordReset(email.trim());
			setSentTo(email.trim());
		} catch (error) {
			setProblem(classifyResetError(error));
		} finally {
			setPending(false);
		}
	};

	const backToSignIn = (
		<p className="text-center text-sm">
			<Link href="/login" className="font-medium text-primary hover:text-primary/90">
				{t("back_to_login")}
			</Link>
		</p>
	);

	if (options.data?.forgot_password === false || problem === "reset_unavailable") {
		return (
			<div className="space-y-4" data-testid="reset-unavailable">
				<AuthNotice tone="error" title={t("reset_unavailable_title")}>
					{t("reset_unavailable")}
				</AuthNotice>
				{backToSignIn}
			</div>
		);
	}

	if (sentTo) {
		return (
			<div className="space-y-4">
				<AuthNotice tone="success" title={t("reset_link_sent_title")}>
					{t("reset_link_sent_body", { email: sentTo, hours: RESET_LINK_LIFETIME_HOURS })}
				</AuthNotice>
				{backToSignIn}
			</div>
		);
	}

	return (
		<form onSubmit={submit} className="space-y-3 md:space-y-4">
			{problem ? <AuthNotice tone="error">{t(problem)}</AuthNotice> : null}
			<div>
				<label htmlFor="email" className="block text-sm font-medium text-foreground">
					{t("email")}
				</label>
				<input
					id="email"
					type="email"
					autoComplete="username"
					required
					maxLength={254}
					placeholder="you@example.com"
					value={email}
					onChange={(e) => setEmail(e.target.value)}
					className={authInputClass}
					disabled={pending}
				/>
			</div>
			<SubmitButton pending={pending}>{t("send_reset_link")}</SubmitButton>
			{backToSignIn}
		</form>
	);
}
