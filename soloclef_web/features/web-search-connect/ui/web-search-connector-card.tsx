"use client";

import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { ConnectorCard } from "@/components/assistant-ui/connector-popup/components/connector-card";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { replaceAddressQuery } from "@/lib/routing/replace-query";
import { connectionOf } from "../api/web-search-connect-api";
import { useWebSearchConnections } from "../hooks/use-web-search-connections";
import {
	CONNECT_PARAM,
	providerToConnect,
	WEB_SEARCH_PROVIDER_INFO,
	type WebSearchProvider,
} from "../model/web-search-providers";
import { WebSearchKeyForm } from "./web-search-key-form";

/**
 * Exa or Tavily in the connectors catalog: a card like Email's that opens in
 * place into its key field, or, once connected, the key's mask, Replace and
 * Disconnect. `?connect=<provider>` (from the research box's "Connect")
 * opens it, scrolls to it and puts the cursor in the field.
 */
export function WebSearchConnectorCard({ provider }: { provider: WebSearchProvider }) {
	const t = useTranslations("web_search_connect");
	const info = WEB_SEARCH_PROVIDER_INFO[provider];
	const { connections, connect, disconnect } = useWebSearchConnections();
	const connection = connectionOf(connections, provider);
	const [expanded, setExpanded] = useState(false);
	const [replacing, setReplacing] = useState(false);
	const [confirming, setConfirming] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [focusField, setFocusField] = useState(false);
	const cardRef = useRef<HTMLDivElement>(null);

	const params = useSearchParams();
	const asked = providerToConnect(new URLSearchParams(params.toString())) === provider;
	useEffect(() => {
		if (!asked) return;
		setExpanded(true);
		setFocusField(true);
		cardRef.current?.scrollIntoView({ block: "center", behavior: "smooth" });
		// Said once: a reload or a back-navigation does not reopen it.
		const next = new URLSearchParams(params.toString());
		next.delete(CONNECT_PARAM);
		const query = next.toString();
		replaceAddressQuery(query);
	}, [asked, params]);

	const saveKey = (apiKey: string) => {
		setError(null);
		connect.mutate(
			{ provider, apiKey },
			{
				onSuccess: () => {
					toast.success(t("connected_toast", { provider: info.title }));
					setReplacing(false);
				},
				onError: (cause) =>
					setError(cause instanceof Error && cause.message ? cause.message : t("connect_failed")),
			}
		);
	};

	const removeKey = () => {
		disconnect.mutate(provider, {
			onSuccess: () => {
				toast.success(t("disconnected_toast", { provider: info.title }));
				setConfirming(false);
			},
			onError: () => toast.error(t("disconnect_failed", { provider: info.title })),
		});
	};

	const toggle = () => setExpanded((now) => !now);
	const showForm = !connection.connected || replacing;

	return (
		<div ref={cardRef} className="flex flex-col gap-2" data-testid={`web-search-card-${provider}`}>
			<ConnectorCard
				id={`${provider}-web-search`}
				title={info.title}
				description={t("card_description", { provider: info.title })}
				connectorType={info.connectorType}
				isConnected={connection.connected}
				statusText={
					connection.connected
						? t("connected_mask", { mask: connection.key_mask ?? "••••" })
						: undefined
				}
				onConnect={toggle}
				onManage={toggle}
			/>
			{expanded && (
				<div className="flex flex-col gap-3 rounded-lg border border-border/60 p-4">
					{showForm ? (
						<WebSearchKeyForm
							provider={provider}
							saving={connect.isPending}
							error={error}
							autoFocus={focusField || replacing}
							onSubmit={saveKey}
							onCancel={replacing ? () => setReplacing(false) : undefined}
						/>
					) : confirming ? (
						<div className="flex flex-col gap-2">
							<p className="text-sm">{t("disconnect_confirm", { provider: info.title })}</p>
							<div className="flex flex-wrap gap-2">
								<Button
									type="button"
									variant="destructive"
									size="sm"
									className="relative"
									disabled={disconnect.isPending}
									onClick={removeKey}
								>
									<span className={disconnect.isPending ? "opacity-0" : ""}>{t("disconnect")}</span>
									{disconnect.isPending && <Spinner size="xs" className="absolute" />}
								</Button>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									disabled={disconnect.isPending}
									onClick={() => setConfirming(false)}
								>
									{t("cancel")}
								</Button>
							</div>
						</div>
					) : (
						<div className="flex flex-col gap-3">
							<p className="text-xs text-muted-foreground">
								{t("connected_note", {
									provider: info.title,
									mask: connection.key_mask ?? "••••",
								})}
							</p>
							<div className="flex flex-wrap gap-2">
								<Button
									type="button"
									variant="outline"
									size="sm"
									onClick={() => {
										setError(null);
										setReplacing(true);
									}}
								>
									{t("replace_key")}
								</Button>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									className="text-destructive hover:text-destructive"
									onClick={() => setConfirming(true)}
								>
									{t("disconnect")}
								</Button>
							</div>
						</div>
					)}
				</div>
			)}
		</div>
	);
}
