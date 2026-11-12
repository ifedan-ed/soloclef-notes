import { baseApiService } from "@/lib/apis/base-api.service";
import { type ChatModel, chatModelSchema } from "./chat-model";

// The chat model is the account's: the server takes the account from the
// session, so the path names no owner and no caller can read another's.
const PATH = "/api/v1/users/me/chat-model";

export const chatModelApi = {
	read: (): Promise<ChatModel> => baseApiService.get(PATH, chatModelSchema),

	/**
	 * `model` is always sent, as a name or as null for the server's default:
	 * the server refuses a body without the field, so that an empty request
	 * can never clear a choice by accident.
	 */
	choose: (model: string | null): Promise<ChatModel> =>
		baseApiService.put(PATH, chatModelSchema, { body: { model } }),
};
