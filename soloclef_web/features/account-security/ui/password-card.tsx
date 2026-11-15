"use client";

import { useAtomValue } from "jotai";
import { CheckCircle2, Info, KeyRound } from "lucide-react";
import { useTranslations } from "next-intl";
import { useEffect, useId, useState } from "react";
import { toast } from "sonner";
import { currentUserAtom } from "@/atoms/user/user-query.atoms";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { useChangePassword } from "../hooks/use-change-password";
import { needsSsoConfirmation, passwordRefusal } from "../lib/password-refusal";
import {
	PASSWORD_MAX,
	PASSWORD_MIN,
	type PasswordProblem,
	passwordFormProblem,
} from "../lib/password-rules";
import {
	CONFIRMED_PARAM,
	cameBackConfirmed,
	SSO_PROOF_MINUTES,
	ssoConfirmationUrl,
} from "../lib/sso-confirmation";

/**
 * Change the account's password, or set its first one.
 *
 * An account with a password gives the current one; the other devices are
 * signed out by the server and this browser stays signed in. An account made
 * through single sign-on has none, and sets one after its provider has
 * confirmed the person in the last ten minutes: when the server asks for
 * that, the page offers to go through the provider and come back
 * (lib/sso-confirmation). This is the phone app's Password screen, with the
 * website's one difference: the browser cannot confirm in a sheet, so the
 * person types the new password again after coming back.
 */
export function PasswordCard({
	hasPassword,
	providerName,
}: {
	hasPassword: boolean;
	/** The single sign-on provider's name, for an account that has no password. */
	providerName: string;
}) {
	const t = useTranslations("account_security");
	const { data: user } = useAtomValue(currentUserAtom);
	const change = useChangePassword();
	const ids = useId();
	const [current, setCurrent] = useState("");
	const [next, setNext] = useState("");
	const [again, setAgain] = useState("");
	const [problem, setProblem] = useState<PasswordProblem | null>(null);
	const [refusal, setRefusal] = useState<ReturnType<typeof passwordRefusal> | null>(null);
	const [mustConfirm, setMustConfirm] = useState(false);
	const [confirmed, setConfirmed] = useState(false);

	// Back from the provider: say so once, then take the marker out of the
	// address so a reload or a bookmark does not claim a confirmation that
	// has since run out.
	useEffect(() => {
		const search = new URLSearchParams(window.location.search);
		if (!cameBackConfirmed(search)) return;
		setConfirmed(true);
		search.delete(CONFIRMED_PARAM);
		const query = search.toString();
		window.history.replaceState(
			window.history.state,
			"",
			`${window.location.pathname}${query ? `?${query}` : ""}${window.location.hash}`
		);
	}, []);

	const submit = (event: React.FormEvent) => {
		event.preventDefault();
		if (change.isPending) return;
		const asked = hasPassword ? current : null;
		const local = passwordFormProblem(asked, next, again, user?.email);
		setProblem(local);
		setRefusal(null);
		if (local) return;
		change.mutate(
			{ current: asked, next },
			{
				onSuccess: (answer) => {
					toast.success(t(hasPassword ? "changed" : "set", { count: answer.signed_out_sessions }));
					setCurrent("");
					setNext("");
					setAgain("");
					setMustConfirm(false);
					setConfirmed(false);
				},
				onError: (error) => {
					if (needsSsoConfirmation(error)) {
						setMustConfirm(true);
						setConfirmed(false);
						return;
					}
					setRefusal(passwordRefusal(error));
				},
			}
		);
	};

	const message = problem
		? t(`problem_${problem}`, { min: PASSWORD_MIN, max: PASSWORD_MAX })
		: refusal
			? t(`refusal_${refusal}`, { min: PASSWORD_MIN })
			: null;
	const filled = (!hasPassword || current !== "") && next !== "" && again !== "";

	return (
		<section className="space-y-4" aria-labelledby={`${ids}-heading`} data-testid="password-card">
			<div className="space-y-1">
				<h3 id={`${ids}-heading`} className="text-base font-semibold tracking-tight">
					{hasPassword ? t("password_heading") : t("set_password_heading")}
				</h3>
				{hasPassword ? (
					<p className="text-sm text-pretty text-muted-foreground">{t("password_intro")}</p>
				) : null}
			</div>

			{!hasPassword && !confirmed && !mustConfirm ? (
				<Alert role="note" data-testid="password-sso-note">
					<Info />
					<AlertTitle>{t("no_password_title")}</AlertTitle>
					<AlertDescription>{t("no_password_body", { provider: providerName })}</AlertDescription>
				</Alert>
			) : null}

			{confirmed && !hasPassword ? (
				<Alert role="status" data-testid="password-sso-confirmed">
					<CheckCircle2 />
					<AlertDescription>
						{t("confirmed_body", { provider: providerName, minutes: SSO_PROOF_MINUTES })}
					</AlertDescription>
				</Alert>
			) : null}

			{mustConfirm ? (
				<Alert role="alert" variant="warning" data-testid="password-sso-confirm">
					<KeyRound />
					<AlertTitle>{t("confirm_title", { provider: providerName })}</AlertTitle>
					<AlertDescription className="space-y-3">
						<p>{t("confirm_body", { provider: providerName })}</p>
						<Button
							type="button"
							size="sm"
							variant="outline"
							onClick={() => {
								// A full-page navigation: the provider's sign-in is a
								// redirect, and its cookie ties the return to this browser.
								window.location.href = ssoConfirmationUrl();
							}}
						>
							{t("confirm_button", { provider: providerName })}
						</Button>
					</AlertDescription>
				</Alert>
			) : null}

			<form onSubmit={submit} className="space-y-4" noValidate>
				{/* A hidden username lets a password manager file the new password
				    under the right account. */}
				<input
					type="email"
					name="username"
					autoComplete="username"
					value={user?.email ?? ""}
					readOnly
					hidden
				/>
				{hasPassword ? (
					<div className="space-y-2">
						<Label htmlFor={`${ids}-current`}>{t("current_label")}</Label>
						<Input
							id={`${ids}-current`}
							type="password"
							autoComplete="current-password"
							maxLength={PASSWORD_MAX}
							value={current}
							onChange={(event) => setCurrent(event.target.value)}
							disabled={change.isPending}
						/>
					</div>
				) : null}
				<div className="space-y-2">
					<Label htmlFor={`${ids}-new`}>{t("new_label")}</Label>
					<Input
						id={`${ids}-new`}
						type="password"
						autoComplete="new-password"
						maxLength={PASSWORD_MAX}
						value={next}
						onChange={(event) => setNext(event.target.value)}
						aria-describedby={`${ids}-hint`}
						disabled={change.isPending}
					/>
				</div>
				<div className="space-y-2">
					<Label htmlFor={`${ids}-again`}>{t("again_label")}</Label>
					<Input
						id={`${ids}-again`}
						type="password"
						autoComplete="new-password"
						maxLength={PASSWORD_MAX}
						value={again}
						onChange={(event) => setAgain(event.target.value)}
						disabled={change.isPending}
					/>
					<p id={`${ids}-hint`} className="text-xs text-muted-foreground">
						{t("rules_hint", { min: PASSWORD_MIN })}
					</p>
				</div>
				{message ? (
					<p role="alert" className="text-sm text-destructive" data-testid="password-problem">
						{message}
					</p>
				) : null}
				<div className="flex justify-end">
					<Button
						type="submit"
						disabled={!filled || change.isPending}
						className="relative min-w-36"
						data-testid="password-save"
					>
						<span className={change.isPending ? "opacity-0" : ""}>
							{hasPassword ? t("change_button") : t("set_button")}
						</span>
						{change.isPending ? <Spinner size="sm" className="absolute" /> : null}
					</Button>
				</div>
			</form>
		</section>
	);
}
