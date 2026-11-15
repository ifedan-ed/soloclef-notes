"use client";

import { useTranslations } from "next-intl";
import { useId } from "react";
import { toast } from "sonner";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { useSignOutEverywhere } from "../hooks/use-sign-out-everywhere";

/**
 * Sign out everywhere: every sign-in of the account ends, this browser's
 * too, and phones stop getting pushes until they sign in again. For a lost
 * phone or a password someone else may know. Set apart in the
 * destructive style Delete account uses on Profile.
 */
export function SignOutEverywhere() {
	const t = useTranslations("account_security");
	const ids = useId();
	const everywhere = useSignOutEverywhere();
	// Once the server has said yes the page is leaving; the button stays busy.
	const busy = everywhere.isPending || everywhere.isSuccess;

	return (
		<section
			className="space-y-3 rounded-lg border border-destructive/30 p-4"
			aria-labelledby={`${ids}-heading`}
			data-testid="sign-out-everywhere"
		>
			<div className="space-y-1">
				<h3 id={`${ids}-heading`} className="text-sm font-medium">
					{t("everywhere_heading")}
				</h3>
				<p className="text-xs text-pretty text-muted-foreground">{t("everywhere_body")}</p>
			</div>
			<AlertDialog>
				<AlertDialogTrigger asChild>
					<Button variant="destructive" size="sm" disabled={busy} className="relative">
						<span className={busy ? "opacity-0" : ""}>{t("everywhere_button")}</span>
						{busy ? <Spinner size="sm" className="absolute" /> : null}
					</Button>
				</AlertDialogTrigger>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>{t("everywhere_confirm_title")}</AlertDialogTitle>
						<AlertDialogDescription>{t("everywhere_confirm_body")}</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
						<AlertDialogAction
							onClick={() =>
								everywhere.mutate(undefined, {
									onError: () => toast.error(t("everywhere_failed")),
								})
							}
							className="bg-destructive text-white hover:bg-destructive/90"
						>
							{t("everywhere_confirm")}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</section>
	);
}
