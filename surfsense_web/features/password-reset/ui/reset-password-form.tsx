"use client";

import Link from "next/link";
import { useTranslations } from "next-intl";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { useHumanCheck } from "@/features/human-check/hooks/use-human-check";
import { resetPassword } from "../api/password-reset-api";
import { classifyResetError, newPasswordProblem, type ResetProblem } from "../lib/reset-errors";
import { readResetToken } from "../lib/reset-token";
import { AuthNotice, authInputClass } from "./auth-notice";
import { SubmitButton } from "./submit-button";

type Problem = ResetProblem | "password_mismatch" | "security_check_pending";

/** Set a new password with the token from the email link. */
export function ResetPasswordForm() {
	const t = useTranslations("auth");
	const check = useHumanCheck("reset_password");
	// undefined until the link has been read (the fragment only exists in the browser).
	const [token, setToken] = useState<string | null | undefined>(undefined);
	const [password, setPassword] = useState("");
	const [confirm, setConfirm] = useState("");
	const [pending, setPending] = useState(false);
	const [done, setDone] = useState(false);
	const [problem, setProblem] = useState<Problem | null>(null);

	useEffect(() => {
		setToken(readResetToken(window.location.hash, window.location.search));
	}, []);

	const submit = async (event: React.FormEvent) => {
		event.preventDefault();
		if (!token) return;
		const local = newPasswordProblem(password, confirm);
		if (local) {
			setProblem(local);
			return;
		}
		if (!check.ready) {
			setProblem(check.unavailable ? "security_check_unavailable" : "security_check_pending");
			return;
		}
		setProblem(null);
		setPending(true);
		try {
			await resetPassword(token, password, check.token);
			setDone(true);
			// The link is spent; keep it out of the address bar and history.
			window.history.replaceState(null, "", window.location.pathname);
		} catch (error) {
			setProblem(classifyResetError(error));
		} finally {
			setPending(false);
			check.reset();
		}
	};

	// Same height before and after the link is read, so nothing jumps.
	if (token === undefined) return <div className="min-h-[18rem]" aria-hidden />;

	if (done) {
		return (
			<div className="space-y-4">
				<AuthNotice tone="success" title={t("reset_success_title")}>
					{t("reset_success_body")}
				</AuthNotice>
				<Button asChild className="w-full">
					<Link href="/login">{t("sign_in")}</Link>
				</Button>
			</div>
		);
	}

	if (!token || problem === "reset_link_invalid") {
		return (
			<div className="space-y-4">
				<AuthNotice tone="error">
					{t(token ? "reset_link_invalid" : "reset_link_missing")}
				</AuthNotice>
				<Button asChild className="w-full">
					<Link href="/forgot-password">{t("request_new_link")}</Link>
				</Button>
			</div>
		);
	}

	return (
		<form onSubmit={submit} className="space-y-3 md:space-y-4">
			{problem ? (
				<AuthNotice tone="error">
					{t(problem === "password_mismatch" ? "passwords_no_match_desc" : problem)}
				</AuthNotice>
			) : null}
			<div>
				<label htmlFor="new-password" className="block text-sm font-medium text-foreground">
					{t("new_password")}
				</label>
				<input
					id="new-password"
					type="password"
					autoComplete="new-password"
					required
					minLength={8}
					maxLength={256}
					value={password}
					onChange={(e) => setPassword(e.target.value)}
					className={authInputClass}
					disabled={pending}
				/>
			</div>
			<div>
				<label htmlFor="confirm-new-password" className="block text-sm font-medium text-foreground">
					{t("confirm_new_password")}
				</label>
				<input
					id="confirm-new-password"
					type="password"
					autoComplete="new-password"
					required
					minLength={8}
					maxLength={256}
					value={confirm}
					onChange={(e) => setConfirm(e.target.value)}
					className={authInputClass}
					disabled={pending}
				/>
			</div>
			{check.widget}
			<SubmitButton pending={pending}>{t("set_new_password")}</SubmitButton>
		</form>
	);
}
