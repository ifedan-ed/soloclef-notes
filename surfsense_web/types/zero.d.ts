import type { Schema } from "@/zero/schema/index";

export type Context =
	| {
			userId: string;
			allowedSpaceIds?: number[];
			/** Workspaces this user owns: they alone read a thread whose creator is gone. */
			ownedSpaceIds?: number[];
	  }
	| undefined;

declare module "@rocicorp/zero" {
	interface DefaultTypes {
		context: Context;
		schema: Schema;
	}
}
