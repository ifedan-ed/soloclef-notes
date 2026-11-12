"use client";

import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { artifactListQueryOptions } from "@/features/artifacts/api/artifact-queries";
import type { ArtifactListItem } from "@/features/artifacts/model/artifact";

export function useArtifactsByDocument(): ReadonlyMap<number, ArtifactListItem> {
	const { data = [] } = useQuery(artifactListQueryOptions());

	return useMemo(() => new Map(data.map((artifact) => [artifact.document_id, artifact])), [data]);
}
