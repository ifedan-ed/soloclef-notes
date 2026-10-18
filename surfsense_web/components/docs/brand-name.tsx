import brand from "@/lib/brand/brand.json";

/** The product name in docs prose, so a rename is one edit in brand.json. */
export function BrandName() {
	return <>{brand.name}</>;
}
