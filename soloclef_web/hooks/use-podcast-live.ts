"use client";

import { type QueryClient, useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import type { PodcastDetail, PodcastSpec, PodcastStatus } from "@/contracts/types/podcast.types";
import { podcastsApiService } from "@/lib/apis/podcasts-api.service";
import { cacheKeys } from "@/lib/query-client/cache-keys";

/**
 * The lifecycle fields a podcast card renders from. Read over REST
 * (GET /podcasts/{id}) and read again whenever the change stream says a
 * Studio output moved (`outputs`, features/live-changes), so the card follows
 * the run without polling.
 */
export interface LivePodcast {
	id: number;
	title: string;
	status: PodcastStatus;
	spec: PodcastSpec | null;
	specVersion: number;
	durationSeconds: number | null;
	error: string | null;
	artifactId: number | null;
	threadId: number | null;
}

interface UsePodcastLiveResult {
	podcast: LivePodcast | undefined;
	isLoading: boolean;
}

export function usePodcastLive(podcastId: number | undefined): UsePodcastLiveResult {
	const { data, isPending } = useQuery({
		queryKey: cacheKeys.podcasts.detail(podcastId ?? 0),
		queryFn: () => podcastsApiService.getDetail(podcastId as number),
		enabled: !!podcastId,
		// A deleted podcast is a state the card shows ("Podcast Unavailable"),
		// not an error to toast.
		meta: { suppressGlobalErrorToast: true },
	});

	const podcast = useMemo<LivePodcast | undefined>(
		() => (podcastId && data ? toLivePodcast(data) : undefined),
		[podcastId, data]
	);

	return { podcast, isLoading: !!podcastId && isPending };
}

/**
 * Every podcast action answers with the podcast as it now is: put it in the
 * cache at once, so the card moves on without waiting for the stream's
 * refetch, and the brief form's next save carries the new spec version.
 */
export function storePodcastDetail(queryClient: QueryClient, detail: PodcastDetail): void {
	queryClient.setQueryData(cacheKeys.podcasts.detail(detail.id), detail);
}

function toLivePodcast(detail: PodcastDetail): LivePodcast {
	return {
		id: detail.id,
		title: detail.title,
		status: detail.status,
		spec: detail.spec,
		specVersion: detail.spec_version,
		durationSeconds: detail.duration_seconds,
		error: detail.error,
		artifactId: detail.artifact_id ?? null,
		threadId: detail.thread_id,
	};
}
