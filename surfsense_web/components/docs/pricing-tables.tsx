import {
	DEFAULT_CATALOG,
	formatCredits,
	formatPrice,
	type PublicCatalog,
} from "@/lib/pricing/catalog";

/**
 * Price tables for the docs, drawn from the same catalog the pricing page
 * uses, so the two never disagree.
 */

const UNIT_LABEL: Record<string, string> = {
	turn: "per message",
	page: "per page",
	image: "per image",
	output: "per output",
	document: "per document",
};

function unitLabel(unit: string): string {
	return UNIT_LABEL[unit] ?? `per ${unit}`;
}

export function PlanTable({ catalog = DEFAULT_CATALOG }: { catalog?: PublicCatalog }) {
	return (
		<table>
			<thead>
				<tr>
					<th>Plan</th>
					<th>Price</th>
					<th>Credits each month</th>
					<th>Includes</th>
				</tr>
			</thead>
			<tbody>
				{catalog.plans.map((plan) => (
					<tr key={plan.slug}>
						<td>{plan.name}</td>
						<td>
							{plan.priceCents === 0
								? formatPrice(0, catalog.currency)
								: `${formatPrice(plan.priceCents, catalog.currency)} / month`}
						</td>
						<td>{formatCredits(plan.monthlyCredits)}</td>
						<td>{plan.features.filter((f) => !/credits every month/i.test(f)).join(" · ")}</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}

export function PackTable({ catalog = DEFAULT_CATALOG }: { catalog?: PublicCatalog }) {
	return (
		<table>
			<thead>
				<tr>
					<th>Top-up</th>
					<th>Price</th>
				</tr>
			</thead>
			<tbody>
				{catalog.packs.map((pack) => (
					<tr key={pack.slug}>
						<td>{formatCredits(pack.credits)} credits</td>
						<td>{formatPrice(pack.priceCents, catalog.currency)}</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}

export function CreditCostTable({ catalog = DEFAULT_CATALOG }: { catalog?: PublicCatalog }) {
	return (
		<table>
			<thead>
				<tr>
					<th>Action</th>
					<th>Credits</th>
				</tr>
			</thead>
			<tbody>
				{catalog.prices.map((price) => (
					<tr key={price.action}>
						<td>{price.label}</td>
						<td>
							{price.credits === 0
								? "Free"
								: `${formatCredits(price.credits)} ${unitLabel(price.unit)}`}
						</td>
					</tr>
				))}
			</tbody>
		</table>
	);
}
