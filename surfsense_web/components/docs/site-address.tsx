import { BRAND, type Brand, siteUrl } from "@/lib/brand";

/**
 * " (`https://notes.example.com/mcp`)" in docs prose: an address on this
 * server, from its configured site URL. Nothing on a server that has none,
 * so the sentence still reads (it already names the path).
 */
export function SiteAddress({ path, brand = BRAND }: { path: string; brand?: Brand }) {
	const url = siteUrl(path, brand);
	return url ? (
		<>
			{" ("}
			<code>{url}</code>
			{")"}
		</>
	) : null;
}
