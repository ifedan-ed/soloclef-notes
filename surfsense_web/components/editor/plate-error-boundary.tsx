"use client";

import { Component, type ReactNode } from "react";
import { reportClientError } from "@/lib/client-error-report";

interface PlateErrorBoundaryProps {
	children: ReactNode;
	fallback: ReactNode;
}

interface PlateErrorBoundaryState {
	hasError: boolean;
}

export class PlateErrorBoundary extends Component<
	PlateErrorBoundaryProps,
	PlateErrorBoundaryState
> {
	constructor(props: PlateErrorBoundaryProps) {
		super(props);
		this.state = { hasError: false };
	}

	static getDerivedStateFromError(): PlateErrorBoundaryState {
		return { hasError: true };
	}

	componentDidCatch(error: unknown) {
		reportClientError("error-boundary", error);
	}

	render() {
		if (this.state.hasError) {
			return this.props.fallback;
		}

		return this.props.children;
	}
}
