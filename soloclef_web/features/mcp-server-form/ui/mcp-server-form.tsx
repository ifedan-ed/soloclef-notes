"use client";

import { XCircle } from "lucide-react";
import { type FormEvent, type ReactNode, useMemo, useRef, useState } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Spinner } from "@/components/ui/spinner";
import type {
	MCPServerConfig,
	MCPTestConnectionResponse,
	MCPTransport,
} from "@/contracts/types/mcp.types";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";
import { useOAuthDiscovery } from "../hooks/use-oauth-discovery";
import { oauthReadiness } from "../model/oauth-readiness";
import {
	type FieldErrors,
	hasErrors,
	type SavedOAuthState,
	type ServerFormState,
	serverConfigFromForm,
	validateServerForm,
} from "../model/server-form-state";
import { AuthFields } from "./auth-fields";
import { ConnectionTest } from "./connection-test";
import { ExtraHeadersField } from "./extra-headers-field";
import { FieldMessage } from "./field-message";

const TRANSPORTS = [
	{ value: "streamable-http", label: "Streamable HTTP" },
	{ value: "sse", label: "SSE" },
] as const;

export interface ServerSubmission {
	name: string;
	serverConfig: MCPServerConfig;
	state: ServerFormState;
}

interface McpServerFormProps {
	initial: ServerFormState;
	/** The saved server's id: its masked secrets then stand for the stored ones. */
	connectorId?: number;
	savedOAuth?: SavedOAuthState | null;
	/** "a public address" or "on the allowed list" (lib/mcp/server-origin). */
	addressRule: string;
	oauthAvailable: boolean;
	disabled?: boolean;
	/** The id an outside button submits the form by (the add view's footer). */
	formId?: string;
	/** Shown as the form's own submit button; omitted when a footer submits it. */
	submitLabel?: string;
	submitting: boolean;
	/** The saved OAuth server's sign-in button. */
	oauthAction?: ReactNode;
	/** Throws with the server's sentence when the save is refused. */
	onSubmit: (submission: ServerSubmission) => Promise<void>;
}

/**
 * An MCP server's settings as a form: name, address, transport, how it is
 * signed in to, and any extra headers. The add view and the edit view are
 * this one form, so a server is described the same way both times.
 *
 * Problems are shown at the field that has them once the person has tried
 * to test or save, and then follow every keystroke, so the form never
 * scolds a field that is still being filled in.
 */
export function McpServerForm({
	initial,
	connectorId,
	savedOAuth,
	addressRule,
	oauthAvailable,
	disabled,
	formId,
	submitLabel,
	submitting,
	oauthAction,
	onSubmit,
}: McpServerFormProps) {
	const [state, setState] = useState<ServerFormState>(initial);
	const [attempted, setAttempted] = useState<"none" | "test" | "submit">("none");
	const [testing, setTesting] = useState(false);
	const [testResult, setTestResult] = useState<MCPTestConnectionResponse | null>(null);
	const [submitError, setSubmitError] = useState<string | null>(null);
	const submittingRef = useRef(false);

	const { discovery, checking } = useOAuthDiscovery(state.url, state.auth === "oauth", connectorId);
	const readiness = oauthReadiness(discovery, checking);

	const errors: FieldErrors = useMemo(() => {
		if (attempted === "none") return {};
		const all = validateServerForm(state, {
			needsClientId: readiness.needsClientId,
			oauthProblem: readiness.problem,
		});
		// A test does not need a name; only saving does.
		if (attempted === "test") delete all.name;
		return all;
	}, [attempted, state, readiness.needsClientId, readiness.problem]);

	const change = (part: Partial<ServerFormState>) => {
		setState((now) => ({ ...now, ...part }));
		// A result for other settings would say something about these.
		setTestResult(null);
		setSubmitError(null);
	};

	const runTest = async () => {
		setAttempted((now) => (now === "submit" ? now : "test"));
		const problems = validateServerForm(state, {
			needsClientId: readiness.needsClientId,
			oauthProblem: readiness.problem,
		});
		delete problems.name;
		if (hasErrors(problems)) return;
		setTesting(true);
		setTestResult(null);
		try {
			setTestResult(
				await connectorsApiService.testMCPConnection(serverConfigFromForm(state), connectorId)
			);
		} catch (error) {
			setTestResult({
				status: "error",
				message: error instanceof Error ? error.message : "The test could not be run.",
				tools: [],
			});
		} finally {
			setTesting(false);
		}
	};

	const submit = async (event: FormEvent) => {
		event.preventDefault();
		if (submittingRef.current || submitting || disabled) return;
		setAttempted("submit");
		const problems = validateServerForm(state, {
			needsClientId: readiness.needsClientId,
			oauthProblem: readiness.problem,
		});
		if (hasErrors(problems)) return;
		submittingRef.current = true;
		setSubmitError(null);
		try {
			await onSubmit({ name: state.name.trim(), serverConfig: serverConfigFromForm(state), state });
		} catch (error) {
			setSubmitError(error instanceof Error ? error.message : "The server could not be saved.");
		} finally {
			submittingRef.current = false;
		}
	};

	// An OAuth server can be tested only once it is saved and signed in: the
	// test runs with the stored sign-in, which an unsaved form does not have.
	const oauthTestable =
		state.auth !== "oauth" || (connectorId !== undefined && Boolean(savedOAuth?.authorized));

	return (
		<form id={formId} onSubmit={submit} noValidate className="flex flex-col gap-5">
			<section className="flex flex-col gap-4 rounded-xl border border-border bg-slate-400/5 p-4 sm:p-6 dark:bg-white/5">
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="mcp-name">Name</Label>
					<Input
						id="mcp-name"
						value={state.name}
						placeholder="e.g. Linear"
						maxLength={120}
						disabled={disabled}
						aria-invalid={Boolean(errors.name) || undefined}
						aria-describedby={errors.name ? "mcp-name-error" : undefined}
						onChange={(event) => change({ name: event.target.value })}
					/>
					<FieldMessage id="mcp-name-error" message={errors.name} />
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="mcp-url">Server URL</Label>
					<Input
						id="mcp-url"
						type="url"
						inputMode="url"
						autoComplete="off"
						spellCheck={false}
						value={state.url}
						placeholder="https://mcp.example.com/mcp"
						disabled={disabled}
						aria-invalid={Boolean(errors.url) || undefined}
						aria-describedby={errors.url ? "mcp-url-error" : "mcp-url-help"}
						onChange={(event) => change({ url: event.target.value })}
					/>
					{errors.url ? (
						<FieldMessage id="mcp-url-error" message={errors.url} />
					) : (
						<p id="mcp-url-help" className="text-xs text-muted-foreground">
							A remote server: https, {addressRule}.
						</p>
					)}
				</div>
				<div className="flex flex-col gap-1.5">
					<span className="text-sm font-medium">Transport</span>
					<SegmentedControl
						ariaLabel="Transport"
						value={state.transport}
						options={TRANSPORTS}
						onValueChange={(value) => change({ transport: value as MCPTransport })}
						className="max-w-full"
					/>
					<p className="text-xs text-muted-foreground">
						Most servers use Streamable HTTP. Choose SSE only if the server's instructions say so.
					</p>
				</div>
			</section>

			<section className="flex flex-col gap-4 rounded-xl border border-border bg-slate-400/5 p-4 sm:p-6 dark:bg-white/5">
				<AuthFields
					state={state}
					errors={errors}
					disabled={disabled}
					oauthAvailable={oauthAvailable}
					readiness={readiness}
					checkingOAuth={checking}
					savedOAuth={savedOAuth}
					oauthAction={oauthAction}
					onChange={change}
				/>
				<ExtraHeadersField
					rows={state.extraHeaders}
					errors={errors}
					disabled={disabled}
					onChange={(extraHeaders) => change({ extraHeaders })}
				/>
			</section>

			<ConnectionTest
				onTest={oauthTestable ? runTest : null}
				blockedReason={
					connectorId === undefined
						? "An OAuth server is tested after it is saved and signed in."
						: "Sign in first: the test runs with your sign-in."
				}
				testing={testing}
				result={testResult}
				disabled={disabled}
			/>

			{submitError && (
				<Alert className="border-red-500/50 bg-red-500/10">
					<XCircle className="size-4 text-red-600" />
					<AlertDescription className="text-xs">{submitError}</AlertDescription>
				</Alert>
			)}

			{submitLabel && (
				<Button
					type="submit"
					className="relative h-9 w-full sm:w-fit sm:self-end"
					disabled={submitting || disabled}
				>
					<span className={submitting ? "opacity-0" : ""}>{submitLabel}</span>
					{submitting && <Spinner size="sm" className="absolute" />}
				</Button>
			)}
		</form>
	);
}
