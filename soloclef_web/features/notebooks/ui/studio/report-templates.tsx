"use client";

import { Check, Pencil } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { notebooksApi } from "../../api/notebooks-api";
import type { ReportTemplate } from "../../model/notebook";

/**
 * Seeded templates (from the server, editable per workspace). The pencil
 * reveals a template's prompt; edits apply to this report, and "Save as
 * default" keeps them for the workspace.
 */
export function ReportTemplates({
	workspaceId,
	templates,
	selectedKey,
	prompt,
	onSelect,
	onPromptChange,
	onSaved,
}: {
	workspaceId: number;
	templates: ReportTemplate[];
	selectedKey: string | null;
	prompt: string | null;
	onSelect: (template: ReportTemplate | null) => void;
	onPromptChange: (prompt: string) => void;
	onSaved: (templates: ReportTemplate[]) => void;
}) {
	const t = useTranslations("notebooks");
	const [editingKey, setEditingKey] = useState<string | null>(null);
	const [saving, setSaving] = useState(false);

	// Unedited seeds are shown in the viewer's language; a workspace's own
	// edit is shown as written.
	const title = (template: ReportTemplate) =>
		template.customized ? template.title : t(`templates.${template.key}.title`);
	const description = (template: ReportTemplate) =>
		template.customized ? template.description : t(`templates.${template.key}.description`);

	const save = async (template: ReportTemplate) => {
		if (!prompt?.trim()) return;
		setSaving(true);
		try {
			onSaved(
				await notebooksApi.saveReportTemplate(workspaceId, template.key, {
					title: template.title,
					description: template.description,
					prompt: prompt.trim(),
				})
			);
			toast.success(t("template_saved"));
		} catch {
			toast.error(t("template_save_failed"));
		} finally {
			setSaving(false);
		}
	};

	if (templates.length === 0) return null;

	return (
		<div className="space-y-2">
			<p className="text-sm font-medium">{t("fields.template")}</p>
			<div className="grid gap-2 sm:grid-cols-2">
				{templates.map((template) => {
					const selected = template.key === selectedKey;
					return (
						<div
							key={template.key}
							className={cn(
								"relative rounded-xl border transition-colors",
								selected ? "border-primary bg-primary/5" : "hover:bg-accent"
							)}
						>
							<button
								type="button"
								aria-pressed={selected}
								onClick={() => onSelect(selected ? null : template)}
								className="flex w-full flex-col gap-1 rounded-xl p-3 pr-10 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
							>
								<span className="flex items-center gap-1.5 text-sm font-medium">
									{selected ? <Check className="size-3.5 text-primary" aria-hidden /> : null}
									{title(template)}
								</span>
								<span className="text-xs leading-4 text-muted-foreground">
									{description(template)}
								</span>
							</button>
							<Button
								type="button"
								variant="ghost"
								size="icon"
								className="absolute right-1.5 top-1.5 size-7 rounded-full"
								aria-label={t("edit_template", { title: title(template) })}
								onClick={() => {
									onSelect(template);
									setEditingKey(editingKey === template.key ? null : template.key);
								}}
							>
								<Pencil className="size-3.5" />
							</Button>
						</div>
					);
				})}
			</div>
			{editingKey && selectedKey === editingKey ? (
				<div className="space-y-2 rounded-xl border p-3">
					<Textarea
						value={prompt ?? ""}
						onChange={(event) => onPromptChange(event.target.value)}
						rows={5}
						maxLength={4000}
						aria-label={t("template_prompt")}
					/>
					<div className="flex justify-end">
						<Button
							variant="outline"
							size="sm"
							disabled={saving || !prompt?.trim()}
							onClick={() => {
								const template = templates.find((item) => item.key === editingKey);
								if (template) void save(template);
							}}
						>
							{t("template_save_default")}
						</Button>
					</div>
				</div>
			) : null}
		</div>
	);
}
