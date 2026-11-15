"use client";

import { useTranslations } from "next-intl";
import { useEffect, useMemo, useState } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import type { PushPreferencesChange, QuietHours } from "@/contracts/types/push-preferences.types";
import { browserTimeZone, supportedTimeZones, timeZoneLabel } from "../lib/browser-time-zone";
import {
	firstQuietHours,
	quietHoursProblem,
	runsOvernight,
	timeZoneChoices,
} from "../lib/quiet-hours";

/**
 * Quiet hours: a switch, then from, until and the time zone. A time is saved
 * when its field is left (a time field changes with every digit typed, and
 * each half-typed time would be a save); the zone is saved when picked.
 * A time the server would refuse is said under the fields and not sent.
 */
export function QuietHoursCard({
	quietHours,
	onChange,
}: {
	quietHours: QuietHours | null;
	onChange: (change: PushPreferencesChange) => void;
}) {
	const t = useTranslations("notification_settings");
	const [draft, setDraft] = useState<QuietHours | null>(quietHours);
	// The stored value replaces the draft whenever it changes: a save shown,
	// the server's answer, or a refusal put back.
	useEffect(() => setDraft(quietHours), [quietHours]);

	const zones = useMemo(
		() => timeZoneChoices(supportedTimeZones(), quietHours?.time_zone ?? null, browserTimeZone()),
		[quietHours?.time_zone]
	);
	const problem = draft ? quietHoursProblem(draft) : null;

	const commit = (next: QuietHours) => {
		if (quietHoursProblem(next) !== null) return;
		if (
			quietHours &&
			next.start === quietHours.start &&
			next.end === quietHours.end &&
			next.time_zone === quietHours.time_zone
		) {
			return;
		}
		onChange({ quiet_hours: next });
	};

	return (
		<section className="rounded-lg border" aria-labelledby="quiet-hours-label">
			<div className="flex items-center justify-between gap-4 p-4">
				<div className="min-w-0 space-y-1">
					<Label id="quiet-hours-label" htmlFor="quiet-hours-enabled">
						{t("quiet_title")}
					</Label>
					<p className="text-sm text-pretty text-muted-foreground">{t("quiet_description")}</p>
				</div>
				<Switch
					id="quiet-hours-enabled"
					checked={quietHours !== null}
					onCheckedChange={(on) =>
						onChange({ quiet_hours: on ? firstQuietHours(browserTimeZone()) : null })
					}
				/>
			</div>
			{draft ? (
				<div className="space-y-3 border-t p-4" data-testid="quiet-hours-fields">
					<div className="grid grid-cols-2 gap-3 sm:grid-cols-[8rem_8rem_minmax(0,1fr)]">
						<div className="space-y-2">
							<Label htmlFor="quiet-hours-start">{t("quiet_start")}</Label>
							<Input
								id="quiet-hours-start"
								type="time"
								step={60}
								required
								value={draft.start}
								aria-invalid={problem !== null || undefined}
								onChange={(event) => setDraft({ ...draft, start: event.target.value })}
								onBlur={() => commit(draft)}
								className="tabular-nums"
							/>
						</div>
						<div className="space-y-2">
							<Label htmlFor="quiet-hours-end">{t("quiet_end")}</Label>
							<Input
								id="quiet-hours-end"
								type="time"
								step={60}
								required
								value={draft.end}
								aria-invalid={problem !== null || undefined}
								onChange={(event) => setDraft({ ...draft, end: event.target.value })}
								onBlur={() => commit(draft)}
								className="tabular-nums"
							/>
						</div>
						<div className="col-span-2 space-y-2 sm:col-span-1">
							<Label htmlFor="quiet-hours-zone">{t("quiet_zone")}</Label>
							<Select
								value={draft.time_zone}
								onValueChange={(zone) => {
									const next = { ...draft, time_zone: zone };
									setDraft(next);
									commit(next);
								}}
							>
								<SelectTrigger id="quiet-hours-zone" className="w-full">
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									{zones.map((zone) => (
										<SelectItem key={zone} value={zone}>
											{timeZoneLabel(zone)}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
					</div>
					<p
						className={problem ? "text-xs text-destructive" : "text-xs text-muted-foreground"}
						role={problem ? "alert" : undefined}
					>
						{problem === "same_time"
							? t("quiet_same_time")
							: problem === "invalid_time"
								? t("quiet_invalid_time")
								: runsOvernight(draft)
									? t("quiet_overnight")
									: t("quiet_same_day")}
					</p>
				</div>
			) : null}
		</section>
	);
}
