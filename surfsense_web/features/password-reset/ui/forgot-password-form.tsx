"use client";

import Link from "next/link";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { useHumanCheck } from "@/features/human-check/hooks/use-human-check";
import { requestPasswordReset } from "../api/password-reset-api";
import { classifyResetError, type ResetProblem } from "../lib/reset-errors";
import { AuthNotice, authInputClass } from "./auth-notice";
import { SubmitButton } from "./submit-button";

/**
 * Ask for a reset link. The confirmation is the same whether or not the
 * address has an account, so the form cannot be used to find out.
 */
export function ForgotPasswordForm() {
	const t = useTranslations("auth");
	const check = useHumanCheck("forgot_password");
	const [email, setEmail] = useState("");
	const [pending, setPending] = useState(false);
	const [sentTo, setSentTo] = useState<string | null>(null);
	const [problem, setProblem] = useState<ResetProblem | "security_check_pending" | null>(null);

	const submit = async (event: React.FormEvent) => {
		event.preventDefault();
		if (!check.ready) {
			setProblem(check.unavailable ? "security_check_unavailable" : "security_check_pending");
			return;
		}
		setProblem(null);
		setPending(true);
		try {
			await requestPasswordReset(email.trim(), check.token);
			setSentTo(email.trim());
		} catch (error) {
			setProblem(classifyResetError(error));
		} finally {
			setPending(false);
			check.reset();
		}
	};

	if (sentTo) {
		return (
			<div className="space-y-4">
				<AuthNotice tone="success" title={t("reset_link_sent_title")}>
					{t("reset_link_sent_body", { email: sentTo })}
				</AuthNotice>
				<p className="text-center text-sm">
					<Link href="/login" className="font-medium text-primary hover:text-primary/90">
						{t("back_to_login")}
					</Link>
				</p>
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
			{check.widget}
			<SubmitButton pending={pending}>{t("send_reset_link")}</SubmitButton>
			<p className="text-center text-sm">
				<Link href="/login" className="font-medium text-primary hover:text-primary/90">
					{t("back_to_login")}
				</Link>
			</p>
		</form>
	);
}
