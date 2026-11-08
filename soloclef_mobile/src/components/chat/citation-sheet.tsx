import { useQuery } from "@tanstack/react-query";
import { View } from "react-native";
import { useApp } from "../../lib/app-context";
import type { CitationToken } from "../../lib/chat/citations";
import { keys } from "../../lib/queries";
import { space } from "../../theme/colors";
import { MarkdownView } from "../markdown-view";
import { Sheet } from "../ui/layout";
import { AppText, DelayedSpinner, Notice } from "../ui/primitives";

/** The cited chunk and its document, like the web's citation panel (mobile preview). */
export function CitationSheet({
	citation,
	onClose,
}: {
	citation: { token: CitationToken; number: number } | null;
	onClose: () => void;
}) {
	const { ep, t } = useApp();
	const chunkId = citation?.token.kind === "chunk" ? citation.token.chunkId : 0;
	const doc = useQuery({
		queryKey: keys.chunk(chunkId),
		queryFn: () => ep.documentByChunk(chunkId),
		enabled: chunkId > 0,
		staleTime: 10 * 60_000,
	});
	const chunk = doc.data?.chunks.find((c) => c.id === chunkId) ?? doc.data?.chunks[0];

	return (
		<Sheet
			visible={!!citation}
			onClose={onClose}
			title={citation ? t("mobile.citation", { number: citation.number }) : undefined}
		>
			{doc.isLoading ? (
				<DelayedSpinner active />
			) : doc.isError ? (
				<Notice
					tone="error"
					message={t("mobile.citation_failed")}
					actionLabel={t("notebooks.retry")}
					onAction={() => doc.refetch()}
				/>
			) : doc.data ? (
				<View style={{ gap: space.md }}>
					<AppText variant="subheading">{doc.data.title}</AppText>
					{chunk ? <MarkdownView text={chunk.content} compact /> : null}
				</View>
			) : null}
		</Sheet>
	);
}
