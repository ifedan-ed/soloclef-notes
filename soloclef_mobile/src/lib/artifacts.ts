/**
 * Reading generated artifacts: the manifest's primary file, and the JSON
 * payloads of the study formats. The flashcard and quiz shapes mirror the
 * web's viewers (features/artifacts/rendering/formats/{flashcards,quiz}/schema.ts); they are
 * validated leniently here so a deck the web would draw also draws here.
 */
import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import type { ArtifactFile, ArtifactManifest } from "./api/schemas";
import { useApp } from "./app-context";

export function primaryFile(manifest: ArtifactManifest | null | undefined): ArtifactFile | undefined {
	return manifest?.files.find((file) => file.role === "primary");
}

export function previewFile(manifest: ArtifactManifest | null | undefined): ArtifactFile | undefined {
	return manifest?.files.find((file) => file.role === "preview");
}

export const flashcardDeckSchema = z.object({
	title: z.string(),
	cards: z
		.array(
			z.object({
				front_text: z.string(),
				back_text: z.string(),
				hint_text: z.string().optional(),
			})
		)
		.min(1),
});
export type FlashcardDeck = z.infer<typeof flashcardDeckSchema>;

export const quizSchema = z.object({
	title: z.string(),
	questions: z
		.array(
			z.object({
				question_text: z.string(),
				options: z.array(z.string()).min(2),
				correct_option_index: z.number().int().min(0),
				explanation_text: z.string(),
			})
		)
		.min(1)
		.refine(
			(questions) => questions.every((q) => q.correct_option_index < q.options.length),
			"answer index out of range"
		),
});
export type Quiz = z.infer<typeof quizSchema>;

const MAX_JSON_BYTES = 15 * 1024 * 1024;

/** The primary file parsed as JSON and checked against ``schema``. */
export function useArtifactJson<T>(workspaceId: number, manifest: ArtifactManifest, schema: z.ZodType<T>) {
	const { api } = useApp();
	const primary = primaryFile(manifest);
	return useQuery({
		queryKey: ["artifact-json", workspaceId, manifest.artifact_id, manifest.generation],
		enabled: !!primary && primary.size_bytes <= MAX_JSON_BYTES,
		staleTime: Number.POSITIVE_INFINITY,
		queryFn: async () => {
			const response = await api.fetchAuthed(primary?.content_url ?? "");
			if (!response.ok) throw new Error(`Could not load (${response.status})`);
			return schema.parse(await response.json());
		},
	});
}

/** Fisher–Yates on a copy; ``random`` is injectable for tests. */
export function shuffled<T>(items: readonly T[], random: () => number = Math.random): T[] {
	const out = [...items];
	for (let i = out.length - 1; i > 0; i -= 1) {
		const j = Math.floor(random() * (i + 1));
		[out[i], out[j]] = [out[j], out[i]];
	}
	return out;
}

/** Quiz scoring: answers are chosen option indexes (or null when skipped). */
export function quizScore(quiz: Quiz, answers: readonly (number | null)[]): number {
	return quiz.questions.reduce((sum, q, i) => sum + (answers[i] === q.correct_option_index ? 1 : 0), 0);
}
