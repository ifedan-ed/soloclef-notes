"use client";

import { useTranslations } from "next-intl";
import { type FC, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { normalizedServerUrl } from "../lib/jmap-mail-outcome";

interface JmapOAuthFormProps {
	onStart: (serverUrl: string) => Promise<void>;
	starting: boolean;
	onCancel: () => void;
}

/**
 * The preferred way in: name the Stalwart server, then sign in on its own
 * page. No password is typed here at all, which is why it comes first.
 */
export const JmapOAuthForm: FC<JmapOAuthFormProps> = ({ onStart, starting, onCancel }) => {
	const t = useTranslations("jmap_mail_connect");
	const [server, setServer] = useState("");
	const serverUrl = normalizedServerUrl(server);
	const serverInvalid = server.trim() !== "" && serverUrl === null;

	return (
		<form
			className="flex flex-col gap-3 rounded-lg border border-border/60 bg-muted/20 p-4"
			data-testid="jmap-oauth-form"
			onSubmit={(event) => {
				event.preventDefault();
				if (serverUrl && !starting) void onStart(serverUrl);
			}}
		>
			<div className="flex flex-col gap-1.5">
				<Label htmlFor="jmap-oauth-server">{t("server_url")}</Label>
				<Input
					id="jmap-oauth-server"
					inputMode="url"
					autoComplete="url"
					placeholder={t("server_url_placeholder")}
					value={server}
					aria-invalid={serverInvalid}
					onChange={(e) => setServer(e.target.value)}
				/>
				{serverInvalid && <p className="text-xs text-destructive">{t("server_url_invalid")}</p>}
			</div>
			<p className="text-xs text-muted-foreground">{t("oauth_hint")}</p>
			<div className="flex flex-wrap items-center gap-2 pt-1">
				<Button type="submit" size="sm" disabled={!serverUrl || starting} className="relative">
					<span className={starting ? "opacity-0" : ""}>{t("oauth_continue")}</span>
					{starting && <Spinner size="xs" className="absolute" />}
				</Button>
				<Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={starting}>
					{t("cancel")}
				</Button>
			</div>
		</form>
	);
};
