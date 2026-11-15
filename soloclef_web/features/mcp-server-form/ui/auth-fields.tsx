"use client";

import { CheckCircle2, CircleAlert, KeyRound } from "lucide-react";
import type { ReactNode } from "react";
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
import type { OAuthReadiness } from "../model/oauth-readiness";
import type {
	AuthChoice,
	FieldErrors,
	SavedOAuthState,
	ServerFormState,
} from "../model/server-form-state";
import { FieldMessage } from "./field-message";
import { SecretInput } from "./secret-input";

const CHOICES: { value: AuthChoice; label: string }[] = [
	{ value: "none", label: "None" },
	{ value: "header", label: "API key in a header" },
	{ value: "bearer", label: "Bearer token" },
	{ value: "oauth", label: "OAuth sign-in" },
];

interface AuthFieldsProps {
	state: ServerFormState;
	errors: FieldErrors;
	disabled?: boolean;
	oauthAvailable: boolean;
	readiness: OAuthReadiness;
	checkingOAuth: boolean;
	/** A saved OAuth server's state, for its signed-in line. */
	savedOAuth?: SavedOAuthState | null;
	/** The saved server's sign-in button, beside its state. */
	oauthAction?: ReactNode;
	onChange: (part: Partial<ServerFormState>) => void;
}

/** How the server is signed in to: one choice, and only that choice's fields. */
export function AuthFields({
	state,
	errors,
	disabled,
	oauthAvailable,
	readiness,
	checkingOAuth,
	savedOAuth,
	oauthAction,
	onChange,
}: AuthFieldsProps) {
	// A saved OAuth server stays editable on a deployment that has since
	// switched OAuth off; it just cannot be newly chosen.
	const oauthOffered = oauthAvailable || state.auth === "oauth";
	return (
		<div className="flex flex-col gap-3">
			<div className="flex flex-col gap-1.5">
				<Label htmlFor="mcp-auth">Authentication</Label>
				<Select
					value={state.auth}
					disabled={disabled}
					onValueChange={(value) => onChange({ auth: value as AuthChoice })}
				>
					<SelectTrigger id="mcp-auth" className="w-full sm:w-72">
						<SelectValue />
					</SelectTrigger>
					<SelectContent>
						{CHOICES.map((choice) => (
							<SelectItem
								key={choice.value}
								value={choice.value}
								disabled={choice.value === "oauth" && !oauthOffered}
							>
								{choice.label}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
				{!oauthAvailable && (
					<p className="text-xs text-muted-foreground">
						OAuth sign-in is not set up on this server yet, so it cannot be chosen.
					</p>
				)}
			</div>

			{state.auth === "header" && (
				<div className="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
					<div className="flex flex-col gap-1.5">
						<Label htmlFor="mcp-header-name">Header name</Label>
						<Input
							id="mcp-header-name"
							value={state.headerName}
							disabled={disabled}
							aria-invalid={Boolean(errors.headerName) || undefined}
							aria-describedby={errors.headerName ? "mcp-header-name-error" : undefined}
							onChange={(event) => onChange({ headerName: event.target.value })}
						/>
						<FieldMessage id="mcp-header-name-error" message={errors.headerName} />
					</div>
					<div className="flex flex-col gap-1.5">
						<Label htmlFor="mcp-header-value">API key</Label>
						<SecretInput
							id="mcp-header-value"
							label="API key"
							placeholder="Paste the key"
							value={state.headerValue}
							disabled={disabled}
							invalid={Boolean(errors.headerValue)}
							describedBy={errors.headerValue ? "mcp-header-value-error" : undefined}
							onChange={(headerValue) => onChange({ headerValue })}
						/>
						<FieldMessage id="mcp-header-value-error" message={errors.headerValue} />
					</div>
				</div>
			)}

			{state.auth === "bearer" && (
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="mcp-bearer">Token</Label>
					<SecretInput
						id="mcp-bearer"
						label="token"
						placeholder="Sent as Authorization: Bearer …"
						value={state.bearerToken}
						disabled={disabled}
						invalid={Boolean(errors.bearerToken)}
						describedBy={errors.bearerToken ? "mcp-bearer-error" : undefined}
						onChange={(bearerToken) => onChange({ bearerToken })}
					/>
					<FieldMessage id="mcp-bearer-error" message={errors.bearerToken} />
				</div>
			)}

			{state.auth === "oauth" && (
				<div className="flex flex-col gap-3 rounded-lg border border-border/60 bg-muted/20 p-3">
					{savedOAuth && (
						<div className="flex flex-wrap items-center justify-between gap-2">
							<p className="flex items-center gap-2 text-sm font-medium">
								{savedOAuth.authorized ? (
									<CheckCircle2 className="size-4 text-emerald-600" />
								) : (
									<CircleAlert className="size-4 text-amber-600" />
								)}
								{savedOAuth.authorized ? "Signed in" : "Not signed in"}
							</p>
							{oauthAction}
						</div>
					)}
					{checkingOAuth ? (
						<p className="flex items-center gap-2 text-xs text-muted-foreground">
							<Spinner size="xs" />
							Checking the server's sign-in…
						</p>
					) : readiness.note ? (
						<p className="flex items-start gap-2 text-xs text-muted-foreground">
							<KeyRound className="mt-0.5 size-3.5 shrink-0" />
							<span className="min-w-0 break-words">{readiness.note}</span>
						</p>
					) : !errors.oauth ? (
						<p className="text-xs text-muted-foreground">
							Enter the server's URL to see how it signs in.
						</p>
					) : null}
					<FieldMessage id="mcp-oauth-error" message={errors.oauth} />
					{readiness.needsClientId && (
						<div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
							<div className="flex flex-col gap-1.5">
								<Label htmlFor="mcp-client-id">Client ID</Label>
								<Input
									id="mcp-client-id"
									value={state.clientId}
									disabled={disabled}
									autoComplete="off"
									aria-invalid={Boolean(errors.clientId) || undefined}
									aria-describedby={errors.clientId ? "mcp-client-id-error" : undefined}
									onChange={(event) => onChange({ clientId: event.target.value })}
								/>
								<FieldMessage id="mcp-client-id-error" message={errors.clientId} />
							</div>
							<div className="flex flex-col gap-1.5">
								<Label htmlFor="mcp-client-secret">
									Client secret <span className="text-muted-foreground">(optional)</span>
								</Label>
								<SecretInput
									id="mcp-client-secret"
									label="client secret"
									value={state.clientSecret}
									disabled={disabled}
									onChange={(clientSecret) => onChange({ clientSecret })}
								/>
							</div>
						</div>
					)}
				</div>
			)}
		</div>
	);
}
