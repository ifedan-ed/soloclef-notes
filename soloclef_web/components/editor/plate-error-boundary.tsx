"use client";

import { Component, type ReactNode } from "react";
import { recoverByReload } from "@/lib/chunk-reload/recover";
import { reportClientError } from "@/lib/client-error-report";

interface PlateErrorBoundaryProps {
	children: ReactNode;
	fallback: ReactNode;
}

interface PlateErrorBoundaryState {
	hasError: boolean;
	reloading: boolean;
}

export class PlateErrorBoundary extends Component<
	PlateErrorBoundaryProps,
	PlateErrorBoundaryState
> {
	constructor(props: PlateErrorBoundaryProps) {
		super(props);
		this.state = { hasError: false, reloading: false };
	}

	static getDerivedStateFromError(): Partial<PlateErrorBoundaryState> {
		return { hasError: true };
	}

	componentDidCatch(error: unknown) {
		// The editor's code is loaded as it is needed; a tab older than the
		// deploy that cannot load it reloads into the new build
		// (lib/chunk-reload), which reports it.
		if (recoverByReload("error-boundary", error)) {
			this.setState({ reloading: true });
			return;
		}
		reportClientError("error-boundary", error);
	}

	render() {
		if (this.state.reloading) return null;
		if (this.state.hasError) {
			return this.props.fallback;
		}

		return this.props.children;
	}
}
