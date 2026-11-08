"use client";

import { useQuery } from "@tanstack/react-query";
import {
	connectorDetailQuery,
	type EditableConnector,
	editableConnector,
	fetchesItsOwnConfig,
} from "@/components/assistant-ui/connector-popup/connector-configs/views/editable-connector";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";

/**
 * The connector an edit view shows: for an MCP server, its config as the
 * server holds it now, asked for when the view opens (editable-connector.ts
 * says why). `null` while nothing is being edited.
 */
export function useEditableConnector(row: SearchSourceConnector | null): EditableConnector | null {
	const asks = row !== null && fetchesItsOwnConfig(row);
	const { data, isError } = useQuery({
		...connectorDetailQuery(row?.id ?? 0),
		enabled: asks,
	});
	if (row === null) return null;
	return editableConnector(row, asks ? data : undefined, isError);
}
