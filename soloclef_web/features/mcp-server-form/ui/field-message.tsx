/** A field's refusal under it, tied to the input by id for screen readers. */
export function FieldMessage({ id, message }: { id: string; message?: string }) {
	if (!message) return null;
	return (
		<p id={id} className="text-xs text-destructive">
			{message}
		</p>
	);
}
