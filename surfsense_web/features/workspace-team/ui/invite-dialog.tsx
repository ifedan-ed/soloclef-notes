"use client";

import { useQueryClient } from "@tanstack/react-query";
import {
	AlertCircle,
	Check,
	Copy,
	Link2,
	Mail,
	MailCheck,
	MailWarning,
	UserPlus,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { type FormEvent, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
	DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import type { Invite } from "@/contracts/types/invites.types";
import type { Role } from "@/contracts/types/roles.types";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { invitesApiService } from "@/lib/apis/invites-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { copyToClipboard } from "@/lib/utils";
import { apiErrorDetail } from "../lib/api-error-detail";
import { assignableRoles, defaultInviteRole } from "../lib/assignable-roles";
import { looksLikeEmail } from "../lib/email-address";
import {
	DEFAULT_INVITE_EXPIRY,
	INVITE_EXPIRY_CHOICES,
	type InviteExpiry,
	inviteExpiresAt,
} from "../lib/invite-expiry";
import { inviteLink } from "../lib/invite-link";

const ROLE_HINT: Record<string, "role_editor_hint" | "role_viewer_hint"> = {
	Editor: "role_editor_hint",
	Viewer: "role_viewer_hint",
};

type Mode = "email" | "link";

/**
 * Invite by address (only that registered account can accept; the server
 * emails it the link) or create an invite link any signed-in account can use.
 */
export function InviteDialog({ workspaceId, roles }: { workspaceId: number; roles: Role[] }) {
	const t = useTranslations("team");
	const queryClient = useQueryClient();
	const offered = useMemo(() => assignableRoles(roles), [roles]);
	const fallbackRole = useMemo(() => defaultInviteRole(roles), [roles]);

	const [open, setOpen] = useState(false);
	const [mode, setMode] = useState<Mode>("email");
	const [maxUses, setMaxUses] = useState("");
	const [email, setEmail] = useState("");
	const [roleId, setRoleId] = useState<string>("");
	const [expiry, setExpiry] = useState<InviteExpiry>(DEFAULT_INVITE_EXPIRY);
	const [label, setLabel] = useState("");
	const [error, setError] = useState<string | null>(null);
	const [sending, setSending] = useState(false);
	const [created, setCreated] = useState<Invite | null>(null);
	const [copied, setCopied] = useState(false);
	const showSending = useDelayedFlag(sending);

	const chosenRoleId = roleId || (fallbackRole ? String(fallbackRole.id) : "");

	const reset = () => {
		setEmail("");
		setMaxUses("");
		setRoleId("");
		setExpiry(DEFAULT_INVITE_EXPIRY);
		setLabel("");
		setError(null);
		setCreated(null);
		setCopied(false);
	};

	const changeOpen = (next: boolean) => {
		setOpen(next);
		if (!next) reset();
	};

	const submit = async (event: FormEvent) => {
		event.preventDefault();
		if (sending) return;
		const address = email.trim();
		if (mode === "email" && !looksLikeEmail(address)) {
			setError(t("email_invalid"));
			return;
		}
		const uses = Number.parseInt(maxUses, 10);
		setError(null);
		setSending(true);
		try {
			const invite = await invitesApiService.createInvite({
				workspace_id: workspaceId,
				data: {
					...(mode === "email" ? { email: address, max_uses: 1 } : {}),
					...(mode === "link" && uses > 0 ? { max_uses: uses } : {}),
					role_id: chosenRoleId ? Number(chosenRoleId) : null,
					expires_at: inviteExpiresAt(expiry),
					...(label.trim() ? { name: label.trim() } : {}),
				},
			});
			setCreated(invite);
			await queryClient.invalidateQueries({
				queryKey: cacheKeys.invites.all(workspaceId.toString()),
			});
		} catch (err) {
			setError(apiErrorDetail(err, t("invite_failed")));
		} finally {
			setSending(false);
		}
	};

	const copyLink = async () => {
		if (!created) return;
		const ok = await copyToClipboard(inviteLink(window.location.origin, created.invite_code));
		setCopied(ok);
	};

	return (
		<Dialog open={open} onOpenChange={changeOpen}>
			<DialogTrigger asChild>
				<Button size="sm" className="gap-1.5">
					<UserPlus className="size-4" />
					{t("invite_button")}
				</Button>
			</DialogTrigger>
			<DialogContent
				className="w-[92vw] max-w-[92vw] p-4 sm:max-w-md md:p-6"
				onOpenAutoFocus={(event) => {
					if (created) event.preventDefault();
				}}
			>
				{created ? (
					<>
						<DialogHeader>
							<DialogTitle className="flex items-center gap-2">
								{!created.email ? (
									<Link2 className="size-5 text-emerald-500" />
								) : created.email_sent === false ? (
									<MailWarning className="size-5 text-amber-500" />
								) : (
									<MailCheck className="size-5 text-emerald-500" />
								)}
								{!created.email
									? t("link_created_title")
									: created.email_sent === false
										? t("not_sent_title")
										: t("sent_title")}
							</DialogTitle>
							<DialogDescription>
								{!created.email
									? t("link_created_body", { role: created.role?.name ?? "" })
									: created.email_sent === false
										? t("not_sent_body", { email: created.email ?? "" })
										: t("sent_body", { email: created.email ?? "" })}
							</DialogDescription>
						</DialogHeader>
						<div className="flex items-center gap-2 rounded-lg bg-muted p-3">
							<code className="min-w-0 flex-1 break-all text-xs">
								{inviteLink(window.location.origin, created.invite_code)}
							</code>
							<Button variant="outline" size="sm" onClick={copyLink} className="shrink-0 gap-1.5">
								{copied ? <Check className="size-4" /> : <Copy className="size-4" />}
								{copied ? t("link_copied") : t("copy_link")}
							</Button>
						</div>
						<DialogFooter className="gap-2">
							<Button variant="secondary" onClick={reset}>
								{t("invite_another")}
							</Button>
							<Button onClick={() => changeOpen(false)}>{t("done")}</Button>
						</DialogFooter>
					</>
				) : (
					<form onSubmit={submit} className="space-y-4" noValidate>
						<DialogHeader>
							<DialogTitle>{t("invite_title")}</DialogTitle>
							<DialogDescription>
								{mode === "email" ? t("invite_description") : t("invite_description_link")}
							</DialogDescription>
						</DialogHeader>
						<fieldset className="grid grid-cols-2 gap-1 rounded-lg bg-muted p-1">
							<legend className="sr-only">{t("invite_title")}</legend>
							{(["email", "link"] as const).map((choice) => (
								<button
									key={choice}
									type="button"
									aria-pressed={mode === choice}
									onClick={() => {
										setMode(choice);
										setError(null);
									}}
									className={
										mode === choice
											? "flex items-center justify-center gap-1.5 rounded-md bg-background px-3 py-1.5 text-sm font-medium shadow-sm"
											: "flex items-center justify-center gap-1.5 rounded-md px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground"
									}
								>
									{choice === "email" ? <Mail className="size-4" /> : <Link2 className="size-4" />}
									{choice === "email" ? t("mode_email") : t("mode_link")}
								</button>
							))}
						</fieldset>
						<div className={mode === "email" ? "space-y-2" : "hidden"}>
							<Label htmlFor="invite-email">{t("email_label")}</Label>
							<Input
								id="invite-email"
								type="email"
								autoComplete="off"
								placeholder={t("email_placeholder")}
								value={email}
								onChange={(event) => {
									setEmail(event.target.value);
									if (error) setError(null);
								}}
								aria-invalid={error ? true : undefined}
								aria-describedby={error ? "invite-error" : undefined}
								required
							/>
						</div>
						<div className="grid gap-4 sm:grid-cols-2">
							<div className="space-y-2">
								<Label htmlFor="invite-role">{t("role_label")}</Label>
								<Select value={chosenRoleId} onValueChange={setRoleId}>
									<SelectTrigger id="invite-role" className="w-full">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										{offered.map((role) => (
											<SelectItem key={role.id} value={String(role.id)}>
												{role.name}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>
							<div className="space-y-2">
								<Label htmlFor="invite-expiry">{t("expiry_label")}</Label>
								<Select value={expiry} onValueChange={(value) => setExpiry(value as InviteExpiry)}>
									<SelectTrigger id="invite-expiry" className="w-full">
										<SelectValue />
									</SelectTrigger>
									<SelectContent>
										{INVITE_EXPIRY_CHOICES.map((choice) => (
											<SelectItem key={choice} value={choice}>
												{t(`expiry_${choice}`)}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
							</div>
						</div>
						{(() => {
							const role = offered.find((r) => String(r.id) === chosenRoleId);
							const hint = role ? ROLE_HINT[role.name] : undefined;
							return hint ? <p className="text-xs text-muted-foreground">{t(hint)}</p> : null;
						})()}
						{mode === "link" && (
							<div className="space-y-2">
								<Label htmlFor="invite-max-uses">{t("max_uses_label")}</Label>
								<Input
									id="invite-max-uses"
									type="number"
									min={1}
									inputMode="numeric"
									placeholder={t("max_uses_placeholder")}
									value={maxUses}
									onChange={(event) => setMaxUses(event.target.value)}
								/>
							</div>
						)}
						<div className="space-y-2">
							<Label htmlFor="invite-label">{t("label_label")}</Label>
							<Input
								id="invite-label"
								maxLength={100}
								placeholder={t("label_placeholder")}
								value={label}
								onChange={(event) => setLabel(event.target.value)}
							/>
						</div>
						{error && (
							<div
								id="invite-error"
								role="alert"
								className="flex items-start gap-2 rounded-lg bg-destructive/10 p-3 text-sm text-destructive"
							>
								<AlertCircle className="mt-0.5 size-4 shrink-0" />
								<span>{error}</span>
							</div>
						)}
						<DialogFooter className="gap-2">
							<Button type="button" variant="secondary" onClick={() => changeOpen(false)}>
								{t("cancel")}
							</Button>
							<Button type="submit" disabled={sending} className="gap-1.5">
								{showSending && <Spinner size="sm" />}
								{sending ? t("sending") : mode === "email" ? t("send") : t("create_link")}
							</Button>
						</DialogFooter>
					</form>
				)}
			</DialogContent>
		</Dialog>
	);
}
