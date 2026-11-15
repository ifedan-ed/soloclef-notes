import { NotesPage } from "@/features/notes/ui/notes-page";

/**
 * Every note, and one note open beside the list at /dashboard/notes/<id>.
 * One optional catch-all rather than two pages, so opening another note keeps
 * the list mounted (its scroll, search and filters) instead of remounting it;
 * the page reads which note is open from the address itself.
 *
 * Written once, as /dashboard/notes; Caddy serves the same file at every
 * /dashboard/notes/<id> (lib/routing/dynamic-segments).
 */
export function generateStaticParams() {
	return [{ note_id: [] }];
}

export default function NotesRoute() {
	return <NotesPage />;
}
