"use client";

import { useQuery } from "@tanstack/react-query";
import { useAtomValue } from "jotai";
import { AlertTriangle, Info } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { myAccessAtom } from "@/atoms/members/members-query.atoms";
import { updateWorkspaceMutationAtom } from "@/atoms/workspaces/workspace-mutation.atoms";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import {
	canEditInstructions,
	instructionsLintApi,
	isOverLimit,
	type LintWarning,
	lintMessage,
	MAX_INSTRUCTIONS_CHARS,
	savedInstructions,
} from "@/features/workspace-instructions";
import { workspacesApiService } from "@/lib/apis/workspaces-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { Spinner } from "../ui/spinner";

interface PromptConfigManagerProps {
	workspaceId: number;
}

export function PromptConfigManager({ workspaceId }: PromptConfigManagerProps) {
	const { data: workspace, isLoading: loading } = useQuery({
		queryKey: cacheKeys.workspaces.detail(workspaceId.toString()),
		queryFn: () => workspacesApiService.getWorkspace({ id: workspaceId }),
		enabled: !!workspaceId,
	});

	const { mutateAsync: updateWorkspace, isPending: isSaving } = useAtomValue(
		updateWorkspaceMutationAtom
	);

	const { data: access } = useAtomValue(myAccessAtom);
	const canEdit = canEditInstructions(access);

	const [customInstructions, setCustomInstructions] = useState("");
	// Save-time warnings; shown inline, never blocking the save.
	const [warnings, setWarnings] = useState<LintWarning[]>([]);
	const [checking, setChecking] = useState(false);
	const hasWorkspace = !!workspace;
	const workspaceInstructions = workspace?.qna_custom_instructions;

	// Initialize state from fetched workspace
	useEffect(() => {
		if (hasWorkspace) {
			setCustomInstructions(workspaceInstructions || "");
		}
	}, [hasWorkspace, workspaceInstructions]);

	// Derive hasChanges during render
	const hasChanges =
		!!workspace && (workspace.qna_custom_instructions || "") !== customInstructions;
	const overLimit = isOverLimit(customInstructions);

	const handleSave = async () => {
		const text = savedInstructions(customInstructions);
		let found: LintWarning[] = [];
		if (text) {
			setChecking(true);
			try {
				found = (await instructionsLintApi.lint(workspaceId, text)).warnings;
			} catch (error: unknown) {
				// The check is advisory; a failed check never stops the save.
				console.warn("Could not check system instructions:", error);
			} finally {
				setChecking(false);
			}
		}
		setWarnings(found);
		try {
			await updateWorkspace({
				id: workspaceId,
				data: { qna_custom_instructions: text },
			});
			toast.success(
				found.length
					? "System instructions saved, with warnings below"
					: "System instructions saved successfully"
			);
		} catch (error: unknown) {
			const message = error instanceof Error ? error.message : "Failed to save system instructions";
			console.error("Error saving system instructions:", error);
			toast.error(message);
		}
	};

	const onSubmit = (e: React.FormEvent) => {
		e.preventDefault();
		if (!canEdit || overLimit) return;
		handleSave();
	};

	if (loading) {
		return (
			<div className="space-y-4 md:space-y-6">
				<div className="space-y-3 md:space-y-4">
					<div className="space-y-2">
						<Skeleton className="h-5 md:h-6 w-36 md:w-48" />
						<Skeleton className="h-3 md:h-4 w-full max-w-md mt-2" />
					</div>
					<div className="space-y-3 md:space-y-4">
						<Skeleton className="h-16 md:h-20 w-full" />
						<Skeleton className="h-24 md:h-32 w-full" />
					</div>
				</div>
			</div>
		);
	}

	return (
		<div className="space-y-4 md:space-y-6">
			<Alert>
				<Info />
				<AlertDescription>
					System instructions shape every answer in this workspace, in chat and in Studio: tone,
					audience, format and length. Answers still come only from your sources, keep their
					citations and follow the safety rules; instructions that try to change those are ignored.
				</AlertDescription>
			</Alert>

			{/* System Instructions Card */}
			<form onSubmit={onSubmit} className="space-y-4 md:space-y-6">
				<div className="space-y-3 md:space-y-4">
					<div className="space-y-1.5 md:space-y-2">
						<h3 className="text-base md:text-lg font-semibold tracking-tight">
							Custom System Instructions
						</h3>
						<p className="text-xs md:text-sm text-muted-foreground">
							Provide specific guidelines for how you want the AI to respond. These instructions
							will be applied to all answers in this workspace.
						</p>
					</div>
					<div className="space-y-3 md:space-y-4">
						<div className="space-y-1.5 md:space-y-2">
							<Label
								htmlFor="custom-instructions-settings"
								className="text-sm md:text-base font-medium"
							>
								Your Instructions
							</Label>
							<Textarea
								id="custom-instructions-settings"
								placeholder="E.g., Always provide practical examples, be concise, focus on technical details, use simple language, respond in a specific format..."
								value={customInstructions}
								onChange={(e) => setCustomInstructions(e.target.value)}
								readOnly={!canEdit}
								aria-invalid={overLimit || undefined}
								rows={10}
								className="resize-none font-mono text-xs md:text-sm"
							/>
							<div className="flex items-center justify-between">
								<p
									className={`text-[10px] md:text-xs ${
										overLimit ? "text-destructive" : "text-muted-foreground"
									}`}
								>
									{savedInstructions(customInstructions).length.toLocaleString()} /{" "}
									{MAX_INSTRUCTIONS_CHARS.toLocaleString()} characters
								</p>
								{canEdit && customInstructions.length > 0 && (
									<Button
										type="button"
										variant="ghost"
										size="sm"
										onClick={() => setCustomInstructions("")}
										className="h-auto py-0.5 md:py-1 px-1.5 md:px-2 text-[10px] md:text-xs"
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

						{!canEdit && access && (
							<Alert>
								<Info />
								<AlertDescription>
									Only the workspace Owner can change these instructions.
								</AlertDescription>
							</Alert>
						)}

						{customInstructions.trim().length === 0 && (
							<Alert>
								<Info />
								<AlertDescription>
									No system instructions are currently set. The AI will use default behavior.
								</AlertDescription>
							</Alert>
						)}
					</div>
				</div>

				{/* Action Buttons */}
				{canEdit && (
					<div className="flex justify-end pt-3 md:pt-4">
						<Button
							type="submit"
							variant="outline"
							disabled={!hasChanges || isSaving || checking || overLimit}
							className="gap-2 bg-white text-black hover:bg-accent hover:text-accent-foreground dark:bg-white dark:text-black"
						>
							{isSaving || checking ? <Spinner size="sm" /> : null}
							{checking ? "Checking" : isSaving ? "Saving" : "Save Instructions"}
						</Button>
					</div>
				)}
			</form>
		</div>
	);
}
