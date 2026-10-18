"""Exa and Tavily as chat tools: one web search on the workspace's own key.

Built per workspace (``definitions.py``) because the key lives in the
workspace's MCP connector; they are not in the global registry, so they have
no REST door. Billed per search on the meter, like the Google Search tool.
"""
