import type { StudioKind } from "../../lib/api/schemas";
import { studioTile } from "../../lib/studio/catalog";
import type { IconName } from "../ui/primitives";

export function tileIcon(kind: StudioKind): IconName {
	return `${studioTile(kind).icon}-outline` as IconName;
}
