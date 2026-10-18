import { BRAND, type Brand, siteUrl } from "@/lib/brand";

interface JsonLdProps {
	data: Record<string, unknown>;
}

export function JsonLd({ data }: JsonLdProps) {
	return (
		<script
			type="application/ld+json"
			// biome-ignore lint/security/noDangerouslySetInnerHtml: JSON-LD built from our own constants.
			dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }}
		/>
	);
}

/**
 * The site as an organisation. `url` and `logo` are left out (JSON drops
 * undefined) when the server has no address, and the contact point when it
 * publishes no support address, so a new server never names another's.
 */
export function organizationData(brand: Brand = BRAND): Record<string, unknown> {
	return {
		"@context": "https://schema.org",
		"@type": "Organization",
		name: brand.name,
		url: siteUrl("/", brand),
		logo: siteUrl("/logo.png", brand),
		description: brand.description,
		...(brand.contactEmail
			? {
					contactPoint: {
						"@type": "ContactPoint",
						email: brand.contactEmail,
						contactType: "customer support",
					},
				}
			: {}),
	};
}

export function OrganizationJsonLd() {
	return <JsonLd data={organizationData()} />;
}

export function WebSiteJsonLd() {
	return (
		<JsonLd
			data={{
				"@context": "https://schema.org",
				"@type": "WebSite",
				name: BRAND.name,
				url: siteUrl("/"),
				description: BRAND.description,
			}}
		/>
	);
}

export function SoftwareApplicationJsonLd() {
	return (
		<JsonLd
			data={{
				"@context": "https://schema.org",
				"@type": "SoftwareApplication",
				name: BRAND.name,
				applicationCategory: "EducationalApplication",
				operatingSystem: "Web, Android, iOS",
				description: BRAND.description,
				url: siteUrl("/"),
				offers: { "@type": "Offer", price: 0, priceCurrency: "USD" },
			}}
		/>
	);
}
