import { STATIC_PLACEHOLDER } from "@/lib/routing/dynamic-segments";
import { NotebookRoute } from "./notebook-route";

// One file for every notebook: it is written once, for the placeholder, and
// Caddy serves it at every /dashboard/notebooks/<id> (lib/routing/dynamic-segments).
export function generateStaticParams() {
	return [{ notebook_id: STATIC_PLACEHOLDER }];
}

export default function NotebookPage() {
	return <NotebookRoute />;
}
