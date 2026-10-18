/** A soft cover colour for a featured card, stable per notebook; no images. */
const COVERS = [
	"bg-amber-100 dark:bg-amber-950/60",
	"bg-sky-100 dark:bg-sky-950/60",
	"bg-emerald-100 dark:bg-emerald-950/60",
	"bg-rose-100 dark:bg-rose-950/60",
	"bg-violet-100 dark:bg-violet-950/60",
	"bg-teal-100 dark:bg-teal-950/60",
	"bg-orange-100 dark:bg-orange-950/60",
	"bg-indigo-100 dark:bg-indigo-950/60",
];

export function notebookCover(notebookId: number): string {
	return COVERS[Math.abs(notebookId) % COVERS.length];
}
