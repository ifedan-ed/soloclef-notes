/**
 * The desktop shell's columns: the workspace rail, then the page. The last
 * track takes the remaining width. A spare track would sit empty to the right
 * of every page (the "blank right half"), with the page squeezed into an
 * auto-sized column beside it. The second sidebar that used to sit between
 * the two is gone: Home is on the rail, the rest in the account menu.
 */
export const DESKTOP_LAYOUT_TRACKS = ["auto", "minmax(0,1fr)"] as const;

export const DESKTOP_LAYOUT_COLUMNS = "grid-cols-[auto_minmax(0,1fr)]";
