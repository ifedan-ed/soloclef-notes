"use client";

import { Check } from "lucide-react";
import { useTranslations } from "next-intl";
import { forwardRef, useMemo } from "react";
import type { MentionedDocumentInfo } from "@/atoms/chat/mentioned-documents.atom";
import { MentionIcon } from "@/components/assistant-ui/mention-icon";
import {
	ComposerSuggestionGroup,
	ComposerSuggestionGroupHeading,
	ComposerSuggestionItem,
	ComposerSuggestionList,
	ComposerSuggestionMessage,
	ComposerSuggestionSkeleton,
} from "@/components/new-chat/composer-suggestion-popup";
import {
	type ComposerSuggestionNavigatorRef,
	type ComposerSuggestionNode,
	useComposerSuggestionNavigator,
} from "@/components/new-chat/use-composer-suggestion-navigator";
import { getMentionDocKey } from "@/lib/chat/mention-doc-key";
import { useNotebookSources } from "../../hooks/use-notebook-queries";
import type { NotebookSource } from "../../model/notebook";

interface NotebookSourcePickerProps {
	workspaceId: number;
	notebookId: number;
	onSelectionChange: (mentions: MentionedDocumentInfo[]) => void;
	onDone: () => void;
	initialSelectedDocuments?: MentionedDocumentInfo[];
	externalSearch?: string;
}

function sourceMention(source: NotebookSource): MentionedDocumentInfo {
	return source.kind === "folder"
		? { id: source.target_id, title: source.title, kind: "folder" }
		: {
				id: source.target_id,
				title: source.title,
				document_type: source.document_type ?? "FILE",
				kind: "doc",
			};
}

/**
 * The @ menu inside a notebook: the notebook's own sources and nothing else.
 * Mentioning one points the answer at that source. The workspace-wide picker
 * (every document, connector and chat) belonged to the retired chat page;
 * picking a connector there mentioned all of its documents at once.
 */
export const NotebookSourcePicker = forwardRef<
	ComposerSuggestionNavigatorRef,
	NotebookSourcePickerProps
>(function NotebookSourcePicker(
	{
		workspaceId,
		notebookId,
		onSelectionChange,
		onDone,
		initialSelectedDocuments = [],
		externalSearch = "",
	},
	ref
) {
	const t = useTranslations("notebooks");
	const sources = useNotebookSources(workspaceId, notebookId);
	const selectedKeys = useMemo(
		() => new Set(initialSelectedDocuments.map(getMentionDocKey)),
		[initialSelectedDocuments]
	);
	const query = externalSearch.trim().toLowerCase();

	const nodes = useMemo<ComposerSuggestionNode<MentionedDocumentInfo>[]>(
		() =>
			(sources.data ?? [])
				.filter((source) => source.enabled)
				.filter((source) => !query || source.title.toLowerCase().includes(query))
				.map((source) => {
					const mention = sourceMention(source);
					return {
						id: getMentionDocKey(mention),
						label: source.title,
						icon: <MentionIcon mention={mention} className="size-4" />,
						type: "item",
						value: mention,
					};
				}),
		[sources.data, query]
	);

	const select = (node: ComposerSuggestionNode<MentionedDocumentInfo>) => {
		const mention = node.value;
		if (!mention) return;
		if (!selectedKeys.has(node.id)) onSelectionChange([...initialSelectedDocuments, mention]);
		onDone();
	};
	const navigator = useComposerSuggestionNavigator({ nodes, onSelect: select, ref });

	return (
		<ComposerSuggestionList ref={navigator.scrollContainerRef} role="listbox" tabIndex={-1}>
			{sources.isLoading ? (
				<ComposerSuggestionSkeleton rows={3} mobileRows={3} />
			) : nodes.length === 0 ? (
				<ComposerSuggestionMessage>
					{query ? t("mention_no_match") : t("mention_no_sources")}
				</ComposerSuggestionMessage>
			) : (
				<ComposerSuggestionGroup>
					<ComposerSuggestionGroupHeading>{t("mention_heading")}</ComposerSuggestionGroupHeading>
					{nodes.map((node, index) => (
						<ComposerSuggestionItem
							key={node.id}
							ref={navigator.getItemRef(index)}
							icon={node.icon}
							selected={index === navigator.highlightedIndex}
							onClick={() => select(node)}
							onMouseEnter={() => navigator.setHighlightedIndex(index)}
						>
							<span className="min-w-0 flex-1 truncate text-sm" title={node.label}>
								{node.label}
							</span>
							{selectedKeys.has(node.id) ? (
								<Check className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
							) : null}
						</ComposerSuggestionItem>
					))}
				</ComposerSuggestionGroup>
			)}
		</ComposerSuggestionList>
	);
});
