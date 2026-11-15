"use client";

import { ExternalLink } from "lucide-react";
import { useTranslations } from "next-intl";
import { type FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { WEB_SEARCH_PROVIDER_INFO, type WebSearchProvider } from "../model/web-search-providers";

interface WebSearchKeyFormProps {
	provider: WebSearchProvider;
	saving: boolean;
	/** The provider's refusal, in its sentence, shown under the field. */
	error: string | null;
	autoFocus?: boolean;
	onSubmit: (apiKey: string) => void;
	onCancel?: () => void;
}

/** The one field a provider needs: its API key, checked with the provider when submitted. */
export function WebSearchKeyForm({
	provider,
	saving,
	error,
	autoFocus,
	onSubmit,
	onCancel,
}: WebSearchKeyFormProps) {
	const t = useTranslations("web_search_connect");
	const info = WEB_SEARCH_PROVIDER_INFO[provider];
	const [key, setKey] = useState("");
	const [blank, setBlank] = useState(false);
	const inputId = `web-search-key-${provider}`;
	const shownError = blank ? t("key_required") : error;

	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (saving) return;
		if (!key.trim()) {
			setBlank(true);
			return;
		}
		onSubmit(key.trim());
	};

	return (
		<form onSubmit={submit} noValidate className="flex flex-col gap-2">
			<Label htmlFor={inputId}>{t("key_label", { provider: info.title })}</Label>
			{/* Stacked: the card is half a grid row wide even on a desktop, too
			    narrow for a key field and a button side by side. */}
			<div className="flex flex-col gap-2">
				<Input
					id={inputId}
					type="password"
					autoComplete="off"
					spellCheck={false}
					autoFocus={autoFocus}
					value={key}
					placeholder={t("key_placeholder")}
					disabled={saving}
					aria-invalid={Boolean(shownError) || undefined}
					aria-describedby={shownError ? `${inputId}-error` : `${inputId}-note`}
					onChange={(event) => {
						setKey(event.target.value);
						setBlank(false);
					}}
				/>
				<div className="flex gap-2">
					<Button type="submit" className="relative h-9" disabled={saving}>
						<span className={saving ? "opacity-0" : ""}>{t("connect")}</span>
						{saving && <Spinner size="xs" className="absolute" />}
					</Button>
					{onCancel && (
						<Button
							type="button"
							variant="ghost"
							className="h-9"
							disabled={saving}
							onClick={onCancel}
						>
							{t("cancel")}
						</Button>
					)}
				</div>
			</div>
			{saving ? (
				<p className="text-xs text-muted-foreground">{t("checking", { provider: info.title })}</p>
			) : shownError ? (
				<p id={`${inputId}-error`} className="text-xs text-destructive">
					{shownError}
				</p>
			) : (
				<p id={`${inputId}-note`} className="text-xs text-muted-foreground">
					{t("key_note", { provider: info.title })}
				</p>
			)}
			<a
				href={info.keyPage}
				target="_blank"
				rel="noopener noreferrer"
				className="inline-flex w-fit items-center gap-1 text-xs text-primary underline-offset-4 hover:underline"
			>
				{t("get_key", { provider: info.title })}
				<ExternalLink className="size-3" />
			</a>
		</form>
	);
}
