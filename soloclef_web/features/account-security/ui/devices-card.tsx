"use client";

import { useTranslations } from "next-intl";
import { useId, useMemo, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import { NotFoundError } from "@/lib/error";
import { useDevices, useSignOutDevice } from "../hooks/use-devices";
import { orderDevices } from "../lib/device-description";
import { leaveToSignIn } from "../lib/leave-to-sign-in";
import type { Device } from "../model/device";
import { DeviceRow } from "./device-row";

/**
 * Signed-in devices: every browser and phone the account is signed in on,
 * this browser first, each with Sign out.
 */
export function DevicesCard() {
	const t = useTranslations("account_security");
	const ids = useId();
	const devices = useDevices();
	const signOut = useSignOutDevice();
	const [leaving, setLeaving] = useState(false);
	const showSkeleton = useDelayedFlag(devices.isPending);
	const rows = useMemo(() => orderDevices(devices.data ?? []), [devices.data]);

	const onSignOut = (device: Device) => {
		if (device.current) {
			setLeaving(true);
			void leaveToSignIn();
			return;
		}
		signOut.mutate(device.session_id, {
			onSuccess: () => toast.success(t("device_signed_out")),
			onError: (error) => {
				// Already ended elsewhere (expired, or signed out from another
				// device): what was asked is done, and the list is read again.
				if (error instanceof NotFoundError) {
					toast.success(t("device_signed_out"));
					return;
				}
				toast.error(t("device_sign_out_failed"));
			},
		});
	};

	return (
		<section className="space-y-3" aria-labelledby={`${ids}-heading`} data-testid="devices-card">
			<div className="space-y-1">
				<h3 id={`${ids}-heading`} className="text-base font-semibold tracking-tight">
					{t("devices_heading")}
				</h3>
				<p className="text-sm text-pretty text-muted-foreground">{t("devices_intro")}</p>
			</div>
			{devices.isError ? (
				<div className="flex flex-col items-start gap-3 rounded-lg border p-4">
					<p role="alert" className="text-sm text-destructive">
						{t("devices_load_failed")}
					</p>
					<Button variant="outline" size="sm" onClick={() => void devices.refetch()}>
						{t("retry")}
					</Button>
				</div>
			) : devices.isPending ? (
				showSkeleton ? (
					<ul className="space-y-2" aria-busy>
						{["a", "b"].map((key) => (
							<li key={key} className="flex items-center gap-3 rounded-lg border p-4">
								<Skeleton className="size-5 shrink-0 rounded" />
								<div className="flex-1 space-y-2">
									<Skeleton className="h-4 w-36" />
									<Skeleton className="h-3 w-64 max-w-full" />
								</div>
							</li>
						))}
					</ul>
				) : null
			) : (
				<ul className="space-y-2" data-testid="devices-list">
					{rows.map((device) => (
						<DeviceRow
							key={device.session_id}
							device={device}
							signingOut={
								(device.current && leaving) ||
								(signOut.isPending && signOut.variables === device.session_id)
							}
							onSignOut={onSignOut}
						/>
					))}
				</ul>
			)}
		</section>
	);
}
