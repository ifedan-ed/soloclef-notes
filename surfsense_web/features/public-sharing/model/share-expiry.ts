/** How long a new public link lives: the choices the share dialog offers. */
export const EXPIRY_CHOICES = ["7", "30", "never"] as const;
export type ExpiryChoice = (typeof EXPIRY_CHOICES)[number];
export const DEFAULT_EXPIRY: ExpiryChoice = "7";

export type ExpiresInDays = 7 | 30 | null;

export function expiryPayload(choice: ExpiryChoice): { expires_in_days: ExpiresInDays } {
	if (choice === "never") return { expires_in_days: null };
	return { expires_in_days: choice === "30" ? 30 : 7 };
}

export function isExpiryChoice(value: string): value is ExpiryChoice {
	return (EXPIRY_CHOICES as readonly string[]).includes(value);
}
