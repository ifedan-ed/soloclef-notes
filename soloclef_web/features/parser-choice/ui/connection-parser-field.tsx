"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import type { DocumentParser } from "@/contracts/types/document.types";
import { useEtlCapabilities } from "@/hooks/use-etl-capabilities";
import { connectorsApiService } from "@/lib/apis/connectors-api.service";
import { connectionParserChoice } from "../connection-parser";
import { PARSER_NAME_KEYS, unavailableReasonKey } from "../parser-labels";
import { ParserPicker } from "./parser-picker";

const card = "rounded-xl bg-slate-400/5 dark:bg-white/5 p-3 sm:p-6 space-y-3";

const parserKey = (connectorId: number) => ["connectors", "parser", connectorId] as const;

/**
 * Which parser reads the files a connection brings in (Drive, Nextcloud).
 *
 * It saves the moment a parser is picked, through its own route, and is not
 * part of the form's Save Changes. That button sends the connection's whole
 * settings to a route that does not know this column; folding the parser
 * into it would mean a parser change could be lost to, or held up by, an
 * unrelated field failing validation.
 *
 * The stored value has its own route, which only the connection's owner
 * may read, so this field is mounted only on the caller's own connections.
 * A connection nobody has chosen for answers null: the picker then shows
 * nothing chosen and says which parser reads its files meanwhile, instead
 * of presenting the server's default as if it had been saved.
 */
export function ConnectionParserField({ connectorId }: { connectorId: number }) {
	const t = useTranslations("upload_documents");
	const queryClient = useQueryClient();
	const capabilities = useEtlCapabilities();
	const stored = useQuery({
		queryKey: parserKey(connectorId),
		queryFn: async (): Promise<DocumentParser | null> =>
			(await connectorsApiService.getConnectorParser(connectorId)).parser,
	});

	const save = useMutation({
		mutationFn: (parser: DocumentParser) =>
			connectorsApiService.setConnectorParser({ connectorId, parser }),
		// Shown as chosen at once; put back if the server refuses it.
		onMutate: async (parser) => {
			await queryClient.cancelQueries({ queryKey: parserKey(connectorId) });
			const previous = queryClient.getQueryData<DocumentParser | null>(parserKey(connectorId));
			queryClient.setQueryData(parserKey(connectorId), parser);
			return { previous };
		},
		onError: (error, _parser, context) => {
			queryClient.setQueryData(parserKey(connectorId), context?.previous ?? null);
			// The server's 400 names the parser and why; show its words.
			toast.error(t("parser_connection_save_failed"), {
				description: error instanceof Error ? error.message : undefined,
			});
		},
		onSuccess: (saved) => {
			queryClient.setQueryData(parserKey(connectorId), saved.parser);
			toast.success(t("parser_connection_saved"));
		},
	});

	const choice = connectionParserChoice(capabilities.data, stored.data ?? null);
	const status =
		capabilities.error || stored.error
			? "failed"
			: capabilities.data && stored.data !== undefined
				? "ready"
				: "loading";

	return (
		<div className={card} data-testid="connection-parser-field">
			<div className="space-y-1">
				<h3 className="font-medium text-sm sm:text-base">{t("parser_connection_label")}</h3>
				<p className="text-xs sm:text-sm text-muted-foreground">{t("parser_connection_desc")}</p>
			</div>
			<ParserPicker
				variant="select"
				status={status}
				choice={choice}
				onChange={(parser) => save.mutate(parser)}
				disabled={save.isPending}
				emptyHint={
					capabilities.data
						? t("parser_connection_unset", {
								parser: t(PARSER_NAME_KEYS[capabilities.data.default_parser]),
							})
						: undefined
				}
			/>
			{choice.storedIsUnavailable && choice.effectiveParser ? (
				<output className="block text-xs text-amber-700 dark:text-amber-400">
					{t(PARSER_NAME_KEYS[choice.effectiveParser])}:{" "}
					{t(
						unavailableReasonKey(
							choice.options.find((option) => option.parser === choice.effectiveParser)
								?.unavailableReason ?? null
						)
					)}
				</output>
			) : null}
		</div>
	);
}
