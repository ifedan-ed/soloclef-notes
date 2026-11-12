// ---------------------------------------------------------------------------
// One save queue a note, for as long as it has words to save
// ---------------------------------------------------------------------------
// A note's autosave is a queue (save-queue.ts): saves go one after another,
// so the server always ends on the newest text. That promise is only as
// wide as the queue's life. When each opening of a note made a queue of its
// own, a note closed and opened again within a save's flight had two: the
// old one still sending " alpha", the new one about to send what the person
// typed next. Two things went wrong, and both lost words:
//
//   - the note opened again from the last text the SERVER had answered, so
//     the words still on their way were not on the screen, and the next
//     keystroke saved the note without them;
//   - the two queues' saves were in flight together, so the older could land
//     last.
//
// Here a note has one queue however many times it is opened, kept until it
// is both closed and fully saved. What is typed is also handed to `onTyped`
// at once (the caller puts it in the note's cache), so a note opened again
// is drawn from the words as typed, saved or not. A save that failed while
// the note was closed is sent again when it is next opened.
// ---------------------------------------------------------------------------

import { createSaveQueue, type NoteChanges, type SaveQueue, type SaveState } from "./save-queue";

export interface NoteSavesOptions<T> {
	/** Quiet time after the last change before it is sent. */
	delayMs: number;
	send: (noteId: number, changes: NoteChanges) => Promise<T>;
	/** What was just typed, before any save: the note as the person has it. */
	onTyped: (noteId: number, changes: NoteChanges) => void;
	/** The server's answer once nothing more of the note is waiting or in flight. */
	onSaved: (noteId: number, saved: T) => void;
	/** The timer, replaceable in tests. */
	schedule?: (run: () => void, ms: number) => unknown;
	cancel?: (handle: unknown) => void;
}

/** A note, open on the screen: its saves, until `close`. */
export interface OpenNoteSaves extends SaveQueue {
	/** The screen is done with the note: what is waiting is sent, and nothing more is reported to it. */
	close: () => void;
}

export interface NoteSaves {
	/** Opens a note's saves; `listen` is told the save state now and as it changes. */
	open: (noteId: number, listen: (state: SaveState) => void) => OpenNoteSaves;
	/** How many notes have a queue: open ones, and closed ones not yet saved. */
	size: () => number;
}

interface Entry {
	queue: SaveQueue;
	state: SaveState;
	/** Who is told the state: the newest opening, or nobody. */
	listen: (state: SaveState) => void;
	/** How many openings have not closed (a remount overlaps the old one's close). */
	open: number;
}

export function createNoteSaves<T>(options: NoteSavesOptions<T>): NoteSaves {
	const entries = new Map<number, Entry>();
	const quiet = () => undefined;

	const entryFor = (noteId: number): Entry => {
		const known = entries.get(noteId);
		if (known) return known;
		const entry: Entry = {
			state: "saved",
			listen: quiet,
			open: 0,
			queue: createSaveQueue<T>({
				delayMs: options.delayMs,
				send: (changes) => options.send(noteId, changes),
				onSaved: (saved) => options.onSaved(noteId, saved),
				onState: (state) => {
					entry.state = state;
					entry.listen(state);
				},
				schedule: options.schedule,
				cancel: options.cancel,
			}),
		};
		entries.set(noteId, entry);
		return entry;
	};

	// Closed and saved: the queue has nothing left to promise.
	const forgetIfDone = (noteId: number, entry: Entry) => {
		if (entry.open === 0 && !entry.queue.hasUnsaved() && entries.get(noteId) === entry) {
			entries.delete(noteId);
		}
	};

	return {
		open(noteId, listen) {
			const entry = entryFor(noteId);
			entry.open += 1;
			entry.listen = listen;
			listen(entry.state);
			// Words a save could not deliver while the note was closed.
			if (entry.queue.hasUnsaved()) void entry.queue.flush();
			let closed = false;
			return {
				queue: (changes) => {
					options.onTyped(noteId, changes);
					entry.queue.queue(changes);
				},
				flush: entry.queue.flush,
				hasUnsaved: entry.queue.hasUnsaved,
				close: () => {
					if (closed) return;
					closed = true;
					entry.open -= 1;
					// A newer opening may already be listening; leave it be.
					if (entry.listen === listen) entry.listen = quiet;
					void entry.queue.flush().then(() => forgetIfDone(noteId, entry));
				},
			};
		},
		size: () => entries.size,
	};
}
