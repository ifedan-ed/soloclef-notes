import { json, number, string, table } from "@rocicorp/zero";

export const folderTable = table("folders")
	.columns({
		id: number(),
		name: string(),
		position: string(),
		parentId: number().optional().from("parent_id"),
		userId: string().from("user_id"),
		createdAt: number().from("created_at"),
		updatedAt: number().from("updated_at"),
		metadata: json<Record<string, unknown>>().optional().from("metadata"),
	})
	.primaryKey("id");
