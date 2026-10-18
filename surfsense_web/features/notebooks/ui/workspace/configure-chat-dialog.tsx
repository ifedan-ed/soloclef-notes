"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { SegmentedControl } from "@/components/ui/segmented-control";
import { Textarea } from "@/components/ui/textarea";
import { useDelayedFlag } from "../../hooks/use-delayed-flag";
import type { ChatGoal, Notebook, ResponseLength } from "../../model/notebook";
import { OptionCards } from "../studio/option-cards";

const GOALS: ChatGoal[] = ["default", "learning_guide", "custom"];
const LENGTHS: ResponseLength[] = ["default", "longer", "shorter"];
export const CHAT_INSTRUCTIONS_MAX_CHARS = 2000;

/** "Configure chat": how this notebook's chats answer, for every chat in it. */
export function ConfigureChatDialog({
	notebook,
	open,
	onOpenChange,
	onSave,
}: {
	notebook: Notebook;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onSave: (changes: {
		chat_goal: ChatGoal;
		chat_instructions: string | null;
		response_length: ResponseLength;
	}) => Promise<unknown>;
}) {
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className="flex max-h-[90vh] w-[calc(100vw-2rem)] max-w-lg flex-col gap-4 overflow-y-auto select-text">
				{open ? (
					<ConfigureChatForm
						notebook={notebook}
						onClose={() => onOpenChange(false)}
						onSave={onSave}
					/>
				) : null}
			</DialogContent>
		</Dialog>
	);
}

function ConfigureChatForm({
	notebook,
	onClose,
	onSave,
}: {
	notebook: Notebook;
	onClose: () => void;
	onSave: Parameters<typeof ConfigureChatDialog>[0]["onSave"];
}) {
	const t = useTranslations("notebooks");
	const [goal, setGoal] = useState<ChatGoal>(notebook.chat_goal);
	const [instructions, setInstructions] = useState(notebook.chat_instructions ?? "");
	const [length, setLength] = useState<ResponseLength>(notebook.response_length);
	const [saving, setSaving] = useState(false);
	const [failed, setFailed] = useState(false);
	const showSaving = useDelayedFlag(saving);
	const needsInstructions = goal === "custom" && !instructions.trim();

	const save = async () => {
		setSaving(true);
		setFailed(false);
		try {
			await onSave({
				chat_goal: goal,
				chat_instructions: instructions.trim() || null,
				response_length: length,
			});
			onClose();
		} catch {
			setFailed(true);
		} finally {
			setSaving(false);
		}
	};

	return (
		<>
			<DialogHeader>
				<DialogTitle>{t("configure_chat.title")}</DialogTitle>
				<DialogDescription>{t("configure_chat.description")}</DialogDescription>
			</DialogHeader>
			<div className="space-y-2">
				<p className="text-sm font-medium">{t("configure_chat.goal")}</p>
				<OptionCards
					label={t("configure_chat.goal")}
					value={goal}
					onChange={(value) => setGoal(value as ChatGoal)}
					options={GOALS.map((value) => ({
						value,
						label: t(`configure_chat.goals.${value}.label`),
						description: t(`configure_chat.goals.${value}.description`),
					}))}
				/>
				{goal === "custom" ? (
					<Textarea
						autoFocus
						aria-label={t("configure_chat.instructions_label")}
						placeholder={t("configure_chat.instructions_placeholder")}
						value={instructions}
						onChange={(event) => setInstructions(event.target.value)}
						maxLength={CHAT_INSTRUCTIONS_MAX_CHARS}
						rows={4}
					/>
				) : null}
			</div>
			<div className="space-y-2">
				<p className="text-sm font-medium">{t("configure_chat.length")}</p>
				<SegmentedControl
					ariaLabel={t("configure_chat.length")}
					value={length}
					onValueChange={(value) => setLength(value as ResponseLength)}
					options={LENGTHS.map((value) => ({
						value,
						label: t(`configure_chat.lengths.${value}`),
					}))}
				/>
			</div>
			{failed ? (
				<p className="text-sm text-destructive" role="alert">
					{t("configure_chat.save_failed")}
				</p>
			) : null}
			<div className="flex justify-end gap-2">
				<Button variant="ghost" onClick={onClose} disabled={saving}>
					{t("cancel")}
				</Button>
				<Button onClick={save} disabled={saving || needsInstructions}>
					{showSaving ? t("saving") : t("save")}
				</Button>
			</div>
		</>
	);
}
