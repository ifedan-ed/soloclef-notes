/**
 * A tool's name as a person reads it: "search_google_drive" is "Search
 * google drive". The chat's tool names are snake_case identifiers chosen for
 * the model, and the settings page shows both: this label to read, and the
 * name itself, because it is what the chat's approval card says.
 *
 * A name with nothing but separators is shown as it is, never as an empty
 * label.
 */
export function toolLabel(name: string): string {
	const words = name.split(/[_\-.:\s]+/).filter(Boolean);
	if (words.length === 0) return name;
	const sentence = words.join(" ").toLowerCase();
	return sentence.charAt(0).toUpperCase() + sentence.slice(1);
}
