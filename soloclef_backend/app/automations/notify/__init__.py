"""Email an automation's owner its run result (``execution.email_result``)."""

from __future__ import annotations

from .delivery import send_run_result_email

__all__ = ["send_run_result_email"]
