"use client";

import { XCircle } from "lucide-react";
import { useTranslations } from "next-intl";
import { type FC, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { AppError } from "@/lib/error";
import type { JmapAccount, NewJmapAccount } from "../api/jmap-mail-api";
import { normalizedServerUrl, serverHost } from "../lib/jmap-mail-outcome";

interface JmapAppPasswordFormProps {
	onSave: (account: NewJmapAccount) => Promise<JmapAccount>;
	onAdded: () => void;
	onCancel: () => void;
}

/**
 * Connect a Stalwart mailbox with an app password, inline. There is no
 * separate test: saving signs in to the server first and stores nothing
 * when that fails, so the server's own reason is shown under the form.
 */
export const JmapAppPasswordForm: FC<JmapAppPasswordFormProps> = ({
	onSave,
	onAdded,
	onCancel,
}) => {
	const t = useTranslations("jmap_mail_connect");
	const [form, setForm] = useState({ label: "", server: "", username: "", app_password: "" });
	const [problem, setProblem] = useState<string | null>(null);
	const [saving, setSaving] = useState(false);

	const set = <K extends keyof typeof form>(key: K, value: (typeof form)[K]) => {
		setForm((prev) => ({ ...prev, [key]: value }));
		setProblem(null);
	};

	const serverUrl = normalizedServerUrl(form.server);
	const serverInvalid = form.server.trim() !== "" && serverUrl === null;
	const payload: NewJmapAccount | null =
		serverUrl && form.username.trim() && form.app_password
			? {
					label: form.label.trim() || undefined,
					server_url: serverUrl,
					username: form.username.trim(),
					// Not trimmed: an app password is used exactly as Stalwart showed it.
					app_password: form.app_password,
				}
			: null;

	const handleSave = async () => {
		if (!payload) return;
		setSaving(true);
		setProblem(null);
		try {
			const created = await onSave(payload);
			// The server's own address, which may not be the one typed.
			toast.success(
				t("account_added_toast", { label: created.label, server: serverHost(created.server_url) })
			);
			onAdded();
		} catch (error) {
			// A refusal (400/422) is the server's sentence about this mailbox;
			// anything else is a fault with nothing useful to show.
			const status = error instanceof AppError ? error.status : undefined;
			setProblem(
				(status === 400 || status === 422) && error instanceof Error && error.message
					? error.message
					: t("save_failed")
			);
		} finally {
			setSaving(false);
		}
	};

	return (
		<div
			className="flex flex-col gap-3 rounded-lg border border-border/60 bg-muted/20 p-4"
			data-testid="jmap-app-password-form"
		>
			<div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
				<div className="flex flex-col gap-1.5 sm:col-span-2">
					<Label htmlFor="jmap-server">{t("server_url")}</Label>
					<Input
						id="jmap-server"
						inputMode="url"
						autoComplete="url"
						placeholder={t("server_url_placeholder")}
						value={form.server}
						aria-invalid={serverInvalid}
						onChange={(e) => set("server", e.target.value)}
					/>
					{serverInvalid && <p className="text-xs text-destructive">{t("server_url_invalid")}</p>}
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="jmap-username">{t("username")}</Label>
					<Input
						id="jmap-username"
						autoComplete="username"
						placeholder={t("username_placeholder")}
						value={form.username}
						onChange={(e) => set("username", e.target.value)}
					/>
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="jmap-app-password">{t("app_password")}</Label>
					<Input
						id="jmap-app-password"
						type="password"
						autoComplete="new-password"
						value={form.app_password}
						onChange={(e) => set("app_password", e.target.value)}
					/>
				</div>
				<div className="flex flex-col gap-1.5 sm:col-span-2">
					<Label htmlFor="jmap-label">{t("label")}</Label>
					<Input
						id="jmap-label"
						placeholder={t("label_placeholder")}
						value={form.label}
						onChange={(e) => set("label", e.target.value)}
					/>
				</div>
			</div>
			<div className="flex flex-col gap-1 text-xs text-muted-foreground">
				<p>{t("app_password_hint")}</p>
				<p>{t("app_password_send_hint")}</p>
			</div>

			{problem && (
				<div className="flex items-start gap-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
					<XCircle className="mt-px size-3.5 shrink-0" />
					<span className="min-w-0 break-words">{problem}</span>
				</div>
			)}

			<div className="flex flex-wrap items-center gap-2 pt-1">
				<Button
					type="button"
					size="sm"
					onClick={handleSave}
					disabled={!payload || saving}
					className="relative"
				>
					<span className={saving ? "opacity-0" : ""}>{t("save")}</span>
					{saving && <Spinner size="xs" className="absolute" />}
				</Button>
				<Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={saving}>
					{t("cancel")}
				</Button>
			</div>
		</div>
	);
};
