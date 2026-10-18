import type {
	ResearchEvent,
	ResearchPhase,
	ResearchProviderStatus,
	ResearchRequest,
	ResearchResult,
} from "../model/research";

/** One source of a several-source search: still looking, answered, or failed. */
export interface ProviderProgress {
	source: string;
	status: ResearchProviderStatus;
	count: number | null;
	code: string | null;
	message: string | null;
}

export type ResearchState =
	| { status: "idle" }
	| {
			status: "searching";
			request: ResearchRequest;
			phase: ResearchPhase | null;
			progress: number;
			results: ResearchResult[];
			/** Empty for a search of one source. */
			providers: ProviderProgress[];
	  }
	| {
			status: "results";
			request: ResearchRequest;
			results: ResearchResult[];
			providers: ProviderProgress[];
	  }
	| {
			status: "error";
			request: ResearchRequest;
			message: string | null;
			/** A known failure the panel can word itself (``source_blocked``, ...). */
			code: string | null;
			/** The source the failure is about, when several were searched. */
			source?: string | null;
	  };

export type ResearchAction =
	| { type: "start"; request: ResearchRequest }
	| { type: "event"; event: ResearchEvent }
	/** The stream closed; without a "done" it still counts if results came. */
	| { type: "closed" }
	| { type: "failed"; message: string | null; code?: string | null }
	| { type: "clear" };

export const idleResearch: ResearchState = { status: "idle" };

/** How one search moves from the query to its results, or to an error. */
export function researchReducer(state: ResearchState, action: ResearchAction): ResearchState {
	switch (action.type) {
		case "start":
			return {
				status: "searching",
				request: action.request,
				phase: null,
				progress: 0,
				results: [],
				providers: [],
			};
		case "clear":
			return idleResearch;
		case "failed":
			return state.status === "searching"
				? {
						status: "error",
						request: state.request,
						message: action.message,
						code: action.code ?? null,
					}
				: state;
		case "closed":
			return state.status === "searching"
				? {
						status: "results",
						request: state.request,
						results: state.results,
						providers: state.providers,
					}
				: state;
		case "event":
			return applyEvent(state, action.event);
	}
}

function applyEvent(state: ResearchState, event: ResearchEvent): ResearchState {
	if (state.status !== "searching") return state;
	switch (event.type) {
		case "phase":
			// Progress never walks backwards, even if phases arrive out of order.
			return { ...state, phase: event.phase, progress: Math.max(state.progress, event.progress) };
		case "results":
			return { ...state, results: event.results };
		case "error":
			return {
				status: "error",
				request: state.request,
				message: event.message || null,
				code: event.code ?? null,
				source: event.source ?? null,
			};
		case "provider":
			return {
				...state,
				providers: withProvider(state.providers, {
					source: event.source,
					status: event.status,
					count: event.count ?? null,
					code: event.code ?? null,
					message: event.message ?? null,
				}),
			};
		case "done":
			return {
				status: "results",
				request: state.request,
				results: state.results,
				providers: state.providers,
			};
	}
}

/** The list with this source's entry replaced, or added in arrival order. */
function withProvider(providers: ProviderProgress[], next: ProviderProgress): ProviderProgress[] {
	const index = providers.findIndex((p) => p.source === next.source);
	if (index === -1) return [...providers, next];
	return providers.map((p, i) => (i === index ? next : p));
}
