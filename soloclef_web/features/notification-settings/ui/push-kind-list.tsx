"use client";

import { useTranslations } from "next-intl";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import type {
	PushPreferences,
	PushPreferencesChange,
} from "@/contracts/types/push-preferences.types";
import { isKindOn } from "../lib/preference-change";

/**
 * One switch per kind of push, in the server's order and with the server's
 * words, so a kind added or retired there needs nothing here.
 */
export function PushKindList({
	preferences,
	onChange,
}: {
	preferences: PushPreferences;
	onChange: (change: PushPreferencesChange) => void;
}) {
	const t = useTranslations("notification_settings");
	return (
		<section className="space-y-3" aria-labelledby="push-kinds-heading">
			<h3 id="push-kinds-heading" className="text-base font-semibold tracking-tight">
				{t("kinds_heading")}
			</h3>
			<ul className="divide-y rounded-lg border">
				{preferences.kinds.map((kind) => {
					const id = `push-kind-${kind.kind}`;
					return (
						<li
							key={kind.kind}
							className="flex items-center justify-between gap-4 p-4"
							data-testid={id}
						>
							<div className="min-w-0 space-y-1">
								<Label htmlFor={id}>{kind.label}</Label>
								<p className="text-sm text-pretty text-muted-foreground">{kind.description}</p>
							</div>
							<Switch
								id={id}
								checked={isKindOn(preferences, kind.kind)}
								onCheckedChange={(on) => onChange({ choices: { [kind.kind]: on } })}
							/>
						</li>
					);
				})}
			</ul>
		</section>
	);
}
