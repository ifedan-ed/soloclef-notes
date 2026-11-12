"use client";

import { authenticatedFetch } from "@/lib/auth-fetch";
import { buildBackendUrl } from "@/lib/env-config";

export interface MemoryLimits {
	soft: number;
	hard: number;
}

export type MemoryLimitLevel = "ok" | "warning" | "error";

export interface MemoryEditorDocument {
	document_id: number;
	title: string;
	document_type: "USER_MEMORY";
	source_markdown: string;
}

interface MemoryReadResponse {
	memory_md?: string;
	limits?: MemoryLimits;
}

/** The account's one memory: what the assistant keeps about the person between chats. */
const MEMORY_PATH = "/api/v1/users/me/memory";

export function getMemoryLimitState(length: number, limits?: MemoryLimits | null) {
	if (!limits) {
		return {
			level: "ok" as MemoryLimitLevel,
			label: `${length.toLocaleString()} chars`,
			isOverLimit: false,
		};
	}

	const isOverLimit = length > limits.hard;
	const isNearLimit = length > limits.soft;
	const level: MemoryLimitLevel = isOverLimit ? "error" : isNearLimit ? "warning" : "ok";
	const suffix = isOverLimit ? " - Exceeds limit" : isNearLimit ? " - Approaching limit" : "";

	return {
		level,
		label: `${length.toLocaleString()}/${limits.hard.toLocaleString()} chars${suffix}`,
		isOverLimit,
	};
}

export async function fetchMemoryEditorDocument({
	title,
	signal,
}: {
	title?: string | null;
	signal?: AbortSignal;
}) {
	const response = await authenticatedFetch(buildBackendUrl(MEMORY_PATH), {
		method: "GET",
		signal,
	});
	if (!response.ok) {
		const errorData = await response.json().catch(() => ({ detail: "Failed to fetch memory" }));
		throw new Error(errorData.detail || "Failed to fetch memory");
	}

	const data = (await response.json()) as MemoryReadResponse;

	return {
		limits: data.limits ?? null,
		document: {
			document_id: -1001,
			title: title || "Memory",
			document_type: "USER_MEMORY",
			source_markdown: data.memory_md ?? "",
		} satisfies MemoryEditorDocument,
	};
}

export async function saveMemoryMarkdown({ markdown }: { markdown: string }) {
	const response = await authenticatedFetch(buildBackendUrl(MEMORY_PATH), {
		method: "PUT",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ memory_md: markdown }),
	});
	if (!response.ok) {
		const errorData = await response.json().catch(() => ({ detail: "Failed to save memory" }));
		throw new Error(errorData.detail || "Failed to save memory");
	}

	const data = (await response.json()) as MemoryReadResponse;

	return {
		markdown: data.memory_md ?? markdown,
		limits: data.limits,
	};
}

/** Empty the account's memory: the assistant starts again knowing nothing about the person. */
export async function resetMemory() {
	const response = await authenticatedFetch(buildBackendUrl(`${MEMORY_PATH}/reset`), {
		method: "POST",
	});
	if (!response.ok) {
		const errorData = await response.json().catch(() => ({ detail: "Failed to reset memory" }));
		throw new Error(errorData.detail || "Failed to reset memory");
	}
	const data = (await response.json()) as MemoryReadResponse;
	return { markdown: data.memory_md ?? "", limits: data.limits };
}
