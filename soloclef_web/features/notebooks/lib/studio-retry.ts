import type { NotebookSource, StudioOutput } from "../model/notebook";
import type { StudioRequestBody } from "./studio-request";

/**
 * The generate request that makes a failed output again: the options it was
 * made with and the notebook sources it was asked for.
 *
 * The sources matter because an output made from some of a notebook's
 * sources would otherwise be retried from every ticked one. A source removed
 * from the notebook since is left out; the server refuses an id that is not
 * the notebook's, so sending it would fail the whole retry. When every one
 * is gone the empty list goes as it is, and the server says there is
 * nothing to make it from, rather than this quietly reading other sources.
 * An output that recorded no sources (one made before they were kept, or in
 * the notebook's chat) sends none, and the ticked sources are read, as
 * before.
 */
export function retryRequest(
	output: StudioOutput,
	/** The notebook's sources, or undefined while they are still being read. */
	sources: NotebookSource[] | undefined,
	requestId: string
): StudioRequestBody {
	const body: StudioRequestBody = {
		...(output.options as Record<string, unknown>),
		kind: output.kind,
		title: output.title,
		request_id: requestId,
	};
	if (output.source_ids) {
		// Not yet read: sent as recorded, and the server refuses by name if
		// one has gone, rather than this guessing that all of them have.
		const present = sources && new Set(sources.map((source) => source.id));
		body.source_ids = present
			? output.source_ids.filter((id) => present.has(id))
			: [...output.source_ids];
	}
	return body;
}
