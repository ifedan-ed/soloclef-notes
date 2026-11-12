"use client";

import { AlertTriangle, Info } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
	useAccountSettings,
	useUpdateAccountSettings,
} from "@/features/account-settings/hooks/use-account-settings";
import { instructionsLintApi } from "../api/lint-api";
import {
	isOverLimit,
	type LintWarning,
	lintMessage,
	MAX_INSTRUCTIONS_CHARS,
	savedInstructions,
} from "../lib/instructions";

/**
 * The account's standing instructions to every chat, and its citations
 * switch: both are how answers are written, so they sit together. A
 * notebook's own instructions still win inside that notebook.
 */
export function InstructionsSettings() {
	const settings = useAccountSettings();
	const update = useUpdateAccountSettings();
	const [draft, setDraft] = useState("");
	// Save-time warnings, shown inline; they never block the save.
	const [warnings, setWarnings] = useState<LintWarning[]>([]);
	const [checking, setChecking] = useState(false);
	const saved = settings.data?.chat_instructions ?? "";

	useEffect(() => {
		if (settings.data) setDraft(settings.data.chat_instructions);
	}, [settings.data]);

	if (settings.isLoading) {
		return (
			<div className="space-y-4">
				<Skeleton className="h-16 w-full" />
				<Skeleton className="h-40 w-full" />
			</div>
		);
	}

	if (settings.isError || !settings.data) {
		return (
			<div className="flex flex-col items-center gap-3 py-8 text-center">
				<p className="text-sm text-destructive">Could not load your instructions.</p>
				<Button variant="outline" size="sm" onClick={() => settings.refetch()}>
					Retry
				</Button>
			</div>
		);
	}

	const overLimit = isOverLimit(draft);
	const hasChanges = savedInstructions(draft) !== saved;
	const citationsEnabled = settings.data.citations_enabled;

	const save = async () => {
		const text = savedInstructions(draft);
		let found: LintWarning[] = [];
		if (text) {
			setChecking(true);
			try {
				found = (await instructionsLintApi.lint(text)).warnings;
			} catch (error: unknown) {
				// The check is advisory: a check that fails never stops the save.
				console.warn("Could not check the instructions:", error);
			} finally {
				setChecking(false);
			}
		}
		setWarnings(found);
		try {
			await update.mutateAsync({ chat_instructions: text || null });
			toast.success(found.length ? "Instructions saved, with warnings below" : "Instructions saved");
		} catch (error: unknown) {
			toast.error(error instanceof Error ? error.message : "Could not save your instructions");
		}
	};

	const toggleCitations = async (next: boolean) => {
		try {
			await update.mutateAsync({ citations_enabled: next });
		} catch (error: unknown) {
			toast.error(error instanceof Error ? error.message : "Could not change citations");
		}
	};

	return (
		<div className="space-y-8">
			<form
				className="space-y-4"
				onSubmit={(event) => {
					event.preventDefault();
					if (!overLimit) void save();
				}}
			>
				<div className="space-y-1.5">
					<h3 className="text-base font-semibold tracking-tight">Instructions for every chat</h3>
					<p className="text-sm text-muted-foreground">
						Tone, audience, format and length: these shape every answer, in chat and in Studio. A
						notebook's own instructions win inside that notebook. Answers still come only from your
						sources, keep their citations and follow the safety rules; instructions that try to
						change those are ignored.
					</p>
				</div>
				<div className="space-y-2">
					<Label htmlFor="chat-instructions">Your instructions</Label>
					<Textarea
						id="chat-instructions"
						placeholder="For example: be concise, use British English, give a worked example for each formula."
						value={draft}
						onChange={(event) => setDraft(event.target.value)}
						aria-invalid={overLimit || undefined}
						rows={10}
						className="min-h-48 resize-y text-sm"
					/>
					<div className="flex items-center justify-between">
						<p className={`text-xs ${overLimit ? "text-destructive" : "text-muted-foreground"}`}>
							{savedInstructions(draft).length.toLocaleString()} /{" "}
							{MAX_INSTRUCTIONS_CHARS.toLocaleString()} characters
						</p>
						{draft.length > 0 && (
							<Button
								type="button"
								variant="ghost"
								size="sm"
								onClick={() => setDraft("")}
								className="h-auto px-2 py-1 text-xs"
							>
								Clear
							</Button>
						)}
					</div>
				</div>

				{overLimit && (
					<p className="text-xs text-destructive" role="alert">
						Keep instructions to {MAX_INSTRUCTIONS_CHARS.toLocaleString()} characters or fewer.
					</p>
				)}

				{warnings.length > 0 && (
					<Alert variant="warning" data-testid="instructions-lint-warnings">
						<AlertTriangle />
						<AlertTitle>Some instructions won't be followed</AlertTitle>
						<AlertDescription>
							<ul className="mt-1 space-y-1.5">
								{warnings.map((warning) => (
									<li key={warning.code}>
										{lintMessage(warning.code)}
										{warning.quote && (
											<span className="block text-muted-foreground">
												&ldquo;{warning.quote}&rdquo;
											</span>
										)}
									</li>
								))}
							</ul>
						</AlertDescription>
					</Alert>
				)}

				{saved.length === 0 && !hasChanges && (
					<Alert>
						<Info />
						<AlertDescription>No instructions yet: answers use the default style.</AlertDescription>
					</Alert>
				)}

				<div className="flex justify-end">
					<Button
						type="submit"
						disabled={!hasChanges || update.isPending || checking || overLimit}
						className="gap-2"
					>
						{update.isPending || checking ? <Spinner size="sm" /> : null}
						{checking ? "Checking" : update.isPending ? "Saving" : "Save instructions"}
					</Button>
				</div>
			</form>

			<div className="flex flex-col gap-3 border-t pt-6 sm:flex-row sm:items-center sm:justify-between">
				<div className="space-y-1">
					<Label htmlFor="citations-enabled">Citations</Label>
					<p className="text-sm text-muted-foreground">
						Number each claim in an answer and link it to the passage it came from.
					</p>
				</div>
				<Switch
					id="citations-enabled"
					checked={citationsEnabled}
					disabled={update.isPending}
					onCheckedChange={toggleCitations}
				/>
			</div>
		</div>
	);
}
