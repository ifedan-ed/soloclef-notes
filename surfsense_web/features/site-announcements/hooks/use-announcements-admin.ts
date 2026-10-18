"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { type AnnouncementBody, announcementsAdminApi } from "../api/announcements-admin-api";

const overviewKey = ["site-announcements", "admin"] as const;

export function useAnnouncementsOverview(enabled: boolean) {
	return useQuery({
		queryKey: overviewKey,
		queryFn: ({ signal }) => announcementsAdminApi.overview(signal),
		enabled,
		staleTime: 15_000,
	});
}

function useAnnouncementWrite<TArgs>(
	fn: (args: TArgs) => Promise<unknown>,
	success: string,
	onDone?: () => void
) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: fn,
		onSuccess: async () => {
			await queryClient.invalidateQueries({ queryKey: overviewKey });
			// The sender is a recipient too; refresh their own badge.
			await queryClient.invalidateQueries({ queryKey: ["notifications"] });
			toast.success(success);
			onDone?.();
		},
		onError: (error) =>
			toast.error(
				error instanceof Error && error.message ? error.message : "Something went wrong."
			),
	});
}

export function useSendAnnouncement(onDone?: () => void) {
	return useAnnouncementWrite(
		(body: AnnouncementBody) => announcementsAdminApi.send(body),
		"Announcement sent",
		onDone
	);
}

export function useWithdrawAnnouncement(onDone?: () => void) {
	return useAnnouncementWrite(
		(id: number) => announcementsAdminApi.withdraw(id),
		"Announcement withdrawn",
		onDone
	);
}
