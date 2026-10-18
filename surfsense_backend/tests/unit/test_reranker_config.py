"""RERANKERS_MODEL_TYPE=litellm builds a Cohere reranker pointed at the proxy."""

from __future__ import annotations

import inspect

import pytest
from rerankers import Document
from rerankers.models.api_rankers import APIRanker

from app import config as config_module

pytestmark = pytest.mark.unit


def test_litellm_type_builds_a_cohere_ranker_on_the_proxy_rerank_route():
    source = inspect.getsource(config_module)
    assert '== "litellm"' in source
    assert 'api_provider="cohere"' in source
    assert 'url=f"{_rerank_base}/rerank"' in source


def test_no_local_reranker_can_be_configured():
    """FlashRank and other local models are gone: any other type is off."""
    source = inspect.getsource(config_module)
    assert "from rerankers import Reranker" not in source
    assert "model_type=RERANKERS_MODEL_TYPE" not in source
    assert "reranking is off" in source


def test_the_cohere_adapter_reads_a_litellm_rerank_reply(monkeypatch):
    """LiteLLM answers in Cohere's shape; the adapter must rank by it."""
    ranker = APIRanker(
        model="cohere-rerank-v4.0-fast",
        api_key="k",
        api_provider="cohere",
        url="http://litellm:4000/v1/rerank",
        verbose=0,
    )
    sent = {}

    class _Reply:
        status_code = 200

        def raise_for_status(self):
            return None

        def json(self):
            return {"results": [{"index": 1, "relevance_score": 0.59},
                                {"index": 0, "relevance_score": 0.18}]}

    def post(url, headers=None, data=None, json=None, **_):
        sent.update(url=url, headers=headers)
        return _Reply()

    import rerankers.models.api_rankers as api

    monkeypatch.setattr(api.requests, "post", post)
    result = ranker.rank(
        query="croup",
        docs=[Document(text="tomatoes", doc_id="a"),
              Document(text="dexamethasone for croup", doc_id="b")],
    )
    assert sent["url"] == "http://litellm:4000/v1/rerank"
    assert sent["headers"]["Authorization"] == "Bearer k"
    assert [r.document.doc_id for r in result.results] == ["b", "a"]
