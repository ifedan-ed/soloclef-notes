"use client";

import { useAtomValue } from "jotai";
import { Trash2 } from "lucide-react";
import { deleteModelConnectionMutationAtom } from "@/atoms/model-connections/model-connections-mutation.atoms";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { ConnectionRead } from "@/contracts/types/model-connections.types";
import { presetForConnection } from "./connection-presets";
import { ConnectionSettingsDialog } from "./connection-settings-dialog";
import { presetIcon } from "./preset-icon";

export function ConnectionCard({ connection }: { connection: ConnectionRead }) {
	const deleteConnection = useAtomValue(deleteModelConnectionMutationAtom);

	// A connection made from a preset is shown as that provider; one of a
	// provider the server no longer supports (null) says so, and can only be
	// removed (connection-presets.tsx).
	const preset = presetForConnection(connection.provider, connection.base_url);
	const providerLabel = preset?.name ?? "Unsupported connection";

	function deleteCurrentConnection() {
		deleteConnection.mutate(connection.id);
	}

	return (
		<div className="overflow-hidden rounded-lg border border-border/60">
			<div className="flex items-center justify-between gap-3 p-4 transition-colors hover:bg-accent">
				<div className="min-w-0">
					<div className="flex items-center gap-2 font-semibold">
						{presetIcon(preset)}
						<span className="truncate">{providerLabel}</span>
						{connection.scope === "global" ? (
							<Badge variant="outline" className="text-[10px]">
								Default
							</Badge>
						) : null}
					</div>
					<div className="truncate text-sm text-muted-foreground">
						{connection.base_url || "Provider default endpoint"}
					</div>
				</div>
				<div className="flex shrink-0 items-center gap-2">
					{preset ? (
						<ConnectionSettingsDialog connection={connection} providerLabel={providerLabel} />
					) : null}
					<AlertDialog>
						<AlertDialogTrigger asChild>
							<Button
								variant="ghost"
								size="icon"
								className="text-muted-foreground hover:text-accent-foreground"
								disabled={deleteConnection.isPending}
								aria-label={`Delete ${providerLabel}`}
							>
								<Trash2 className="h-4 w-4" />
							</Button>
						</AlertDialogTrigger>
						<AlertDialogContent>
							<AlertDialogHeader>
								<AlertDialogTitle>Delete this provider?</AlertDialogTitle>
								<AlertDialogDescription>
									<span className="font-medium text-foreground">{providerLabel}</span> and all of
									its models will be removed from your account. This cannot be undone.
								</AlertDialogDescription>
							</AlertDialogHeader>
							<AlertDialogFooter>
								<AlertDialogCancel disabled={deleteConnection.isPending}>Cancel</AlertDialogCancel>
								<AlertDialogAction
									onClick={deleteCurrentConnection}
									disabled={deleteConnection.isPending}
									className="bg-destructive text-white hover:bg-destructive/90"
								>
									Delete
								</AlertDialogAction>
							</AlertDialogFooter>
						</AlertDialogContent>
					</AlertDialog>
				</div>
			</div>
		</div>
	);
}
