"""``browserbase.read_page``, built for one workspace.

Charged per page that came back with text on the ``web_crawl`` meter (0.2
credits by default), to the workspace's owner, exactly like the research
box's "Read a web page" with Browserbase.
"""

from __future__ import annotations

from app.capabilities.core import ActivityDescriptor, BillingUnit, Capability

from .executor import build_read_page_executor
from .schemas import ReadPageInput, ReadPageOutput


def read_page_capability(workspace_id: int) -> Capability:
    return Capability(
        name="browserbase.read_page",
        description=(
            "Open one public web page in a real cloud browser (Browserbase) and "
            "return its text as markdown. Works on pages built with JavaScript. "
            "One page per call."
        ),
        input_schema=ReadPageInput,
        output_schema=ReadPageOutput,
        executor=build_read_page_executor(workspace_id),
        billing_unit=BillingUnit.WEB_CRAWL,
        activity=ActivityDescriptor(
            active_title="Opening the page in a browser",
            completed_title="Read the page in a browser",
            category="research",
            icon_key="scan-text",
            integration_key="browserbase",
        ),
    )
