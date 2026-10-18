"use client";

import { CheckCircle2, XCircle } from "lucide-react";
import { useTranslations } from "next-intl";
import { type FC, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import {
	type EmailImapAccount,
	emailImapApi,
	type NewEmailImapAccount,
} from "../api/email-imap-api";

interface EmailImapAddAccountFormProps {
	workspaceId: number;
	onSave: (account: NewEmailImapAccount) => Promise<EmailImapAccount>;
	onAdded: (label: string) => void;
	onCancel: () => void;
}

/**
 * Inline add-account form (no dialog, no browser popups). "Test connection"
 * checks the settings live, before anything is saved; "Save" persists it
 * (the backend encrypts the password the moment it arrives).
 */
export const EmailImapAddAccountForm: FC<EmailImapAddAccountFormProps> = ({
	workspaceId,
	onSave,
	onAdded,
	onCancel,
}) => {
	const t = useTranslations("email_imap_connect");
	const [form, setForm] = useState({
		label: "",
		host: "",
		port: "993",
		use_tls: true,
		username: "",
		app_password: "",
	});
	const [testing, setTesting] = useState(false);
	const [testResult, setTestResult] = useState<
		{ ok: true; folderCount: number } | { ok: false; message: string } | null
	>(null);
	const [saving, setSaving] = useState(false);

	const set = <K extends keyof typeof form>(key: K, value: (typeof form)[K]) => {
		setForm((prev) => ({ ...prev, [key]: value }));
		setTestResult(null);
	};

	const asPayload = (): NewEmailImapAccount | null => {
		const port = Number.parseInt(form.port, 10);
		if (!form.host.trim() || !form.username.trim() || !form.app_password.trim()) return null;
		if (!Number.isFinite(port) || port < 1 || port > 65535) return null;
		return {
			label: form.label.trim() || form.username.trim(),
			host: form.host.trim(),
			port,
			use_tls: form.use_tls,
			username: form.username.trim(),
			app_password: form.app_password,
		};
	};

	const handleTest = async () => {
		const payload = asPayload();
		if (!payload) return;
		setTesting(true);
		setTestResult(null);
		try {
			const result = await emailImapApi.testUnsaved(workspaceId, payload);
			if (result.status === "ok") {
				setTestResult({ ok: true, folderCount: result.folder_count ?? 0 });
			} else {
				setTestResult({ ok: false, message: result.message ?? "" });
			}
		} catch {
			setTestResult({ ok: false, message: "" });
		} finally {
			setTesting(false);
		}
	};

	const handleSave = async () => {
		const payload = asPayload();
		if (!payload) return;
		setSaving(true);
		try {
			const created = await onSave(payload);
			toast.success(t("account_added_toast", { label: created.label }));
			onAdded(created.label);
		} catch {
			toast.error(t("save_failed"));
		} finally {
			setSaving(false);
		}
	};

	const canSubmit = asPayload() !== null;

	return (
		<div className="flex flex-col gap-3 rounded-lg border border-border/60 bg-muted/20 p-4">
			<div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="email-imap-label">{t("label")}</Label>
					<Input
						id="email-imap-label"
						placeholder={t("label_placeholder")}
						value={form.label}
						onChange={(e) => set("label", e.target.value)}
					/>
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="email-imap-host">{t("host")}</Label>
					<Input
						id="email-imap-host"
						placeholder={t("host_placeholder")}
						value={form.host}
						onChange={(e) => set("host", e.target.value)}
					/>
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="email-imap-port">{t("port")}</Label>
					<Input
						id="email-imap-port"
						type="number"
						min={1}
						max={65535}
						value={form.port}
						onChange={(e) => set("port", e.target.value)}
					/>
				</div>
				<div className="flex items-center justify-between gap-2 pt-5 sm:pt-0 sm:self-end sm:pb-1.5">
					<Label htmlFor="email-imap-tls">{t("use_tls")}</Label>
					<Switch
						id="email-imap-tls"
						checked={form.use_tls}
						onCheckedChange={(checked) => set("use_tls", checked)}
					/>
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="email-imap-username">{t("username")}</Label>
					<Input
						id="email-imap-username"
						placeholder={t("username_placeholder")}
						value={form.username}
						onChange={(e) => set("username", e.target.value)}
					/>
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="email-imap-password">{t("app_password")}</Label>
					<Input
						id="email-imap-password"
						type="password"
						autoComplete="new-password"
						value={form.app_password}
						onChange={(e) => set("app_password", e.target.value)}
					/>
				</div>
			</div>
			<p className="text-xs text-muted-foreground">{t("app_password_hint")}</p>

			{testResult && (
				<div
					className={`flex items-center gap-2 rounded-md px-3 py-2 text-xs ${
						testResult.ok
							? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
							: "bg-destructive/10 text-destructive"
					}`}
				>
					{testResult.ok ? (
						<>
							<CheckCircle2 className="size-3.5 shrink-0" />
							{t("test_success", { count: testResult.folderCount })}
						</>
					) : (
						<>
							<XCircle className="size-3.5 shrink-0" />
							{t("test_failed", { message: testResult.message })}
						</>
					)}
				</div>
			)}

			<div className="flex flex-wrap items-center gap-2 pt-1">
				<Button
					type="button"
					variant="outline"
					size="sm"
					onClick={handleTest}
					disabled={!canSubmit || testing || saving}
					className="relative"
				>
					<span className={testing ? "opacity-0" : ""}>{t("test_connection")}</span>
					{testing && <Spinner size="xs" className="absolute" />}
				</Button>
				<Button
					type="button"
					size="sm"
					onClick={handleSave}
					disabled={!canSubmit || saving || testing}
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
