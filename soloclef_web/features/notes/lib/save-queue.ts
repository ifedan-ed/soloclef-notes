// ---------------------------------------------------------------------------
// Autosave, as a queue
// ---------------------------------------------------------------------------
// A note saves itself as it is typed. Each keystroke merges into the changes
// waiting to be sent, and they are sent once typing has paused for
// `delayMs`. Saves go one after another, never two at once, so the server
// always ends on the newest text: a slow save cannot land after a faster,
// later one. A save that fails keeps its changes (under anything typed
// since) and is sent again with the next one.
//
// The server does the other half of keeping indexing cheap: a save that
// changes the text queues one delayed indexing job, and saves made while it
// waits queue nothing (soloclef_api/internal/notes). So typing for a minute
// is about sixty small writes and four indexing runs, not sixty.
// ---------------------------------------------------------------------------

export type SaveState = "saved" | "unsaved" | "saving" | "failed";

export interface NoteChanges {
	title?: string;
	content?: string;
}

export interface SaveQueueOptions<T> {
	/** Quiet time after the last change before it is sent. */
	delayMs: number;
	send: (changes: NoteChanges) => Promise<T>;
	/** The server's answer to the newest save sent. */
	onSaved: (saved: T) => void;
	onState: (state: SaveState) => void;
	/** The timer, replaceable in tests. */
	schedule?: (run: () => void, ms: number) => unknown;
	cancel?: (handle: unknown) => void;
}

export interface SaveQueue {
	/** Adds changes; they are sent once typing pauses. */
	queue: (changes: NoteChanges) => void;
	/** Sends what is waiting now; resolves once every save so far has been answered. */
	flush: () => Promise<void>;
	/** Whether anything typed has not reached the server yet. */
	hasUnsaved: () => boolean;
}

export function createSaveQueue<T>(options: SaveQueueOptions<T>): SaveQueue {
	const schedule = options.schedule ?? ((run, ms) => setTimeout(run, ms));
	const cancel =
		options.cancel ?? ((handle) => clearTimeout(handle as ReturnType<typeof setTimeout>));
	let waiting: NoteChanges = {};
	let timer: unknown = null;
	let sending = 0;
	let failed = false;
	let chain: Promise<void> = Promise.resolve();

	const isEmpty = (changes: NoteChanges) =>
		changes.title === undefined && changes.content === undefined;
	const settle = () => {
		if (sending > 0) options.onState("saving");
		else if (!isEmpty(waiting)) options.onState(failed ? "failed" : "unsaved");
		else options.onState("saved");
	};

	const flush = (): Promise<void> => {
		if (timer !== null) cancel(timer);
		timer = null;
		if (isEmpty(waiting)) return chain;
		const changes = waiting;
		waiting = {};
		sending += 1;
		failed = false;
		settle();
		chain = chain.then(() =>
			options.send(changes).then(
				(saved) => {
					sending -= 1;
					// Only the newest answer describes the note as it now is.
					if (sending === 0 && isEmpty(waiting)) options.onSaved(saved);
					settle();
				},
				() => {
					sending -= 1;
					// Kept, under anything typed since, for the next save.
					waiting = { ...changes, ...waiting };
					failed = true;
					settle();
				}
			)
		);
		return chain;
	};

	return {
		queue: (changes) => {
			waiting = { ...waiting, ...changes };
			if (timer !== null) cancel(timer);
			timer = schedule(() => void flush(), options.delayMs);
			settle();
		},
		flush,
		hasUnsaved: () => !isEmpty(waiting) || sending > 0,
	};
}
