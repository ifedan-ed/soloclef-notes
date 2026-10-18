"""The shared models' tier in the pickers follows the billing admin's choice.

The pickers label a premium model "Premium"; billing charges by the tier the
admin set (Billing -> Admin -> Model tiers), so the label must follow that
setting rather than only the YAML's ``billing_tier``.
"""

from __future__ import annotations

import pytest

from app.billing.persistence.models import BillingSetting
from app.routes.model_connections_routes import list_global_connections
from app.services.global_model_catalog import materialize_global_model_catalog

pytestmark = pytest.mark.unit


class _Settings:
    def __init__(self, rows):
        self._rows = rows

    async def execute(self, *_args, **_kwargs):
        rows = self._rows

        class _Result:
            def scalars(self):
                return self

            def all(self):
                return rows

        return _Result()


def _config(config_id: int, model_name: str, tier: str) -> dict:
    return {
        "id": config_id,
        "model_name": model_name,
        "provider": "OPENAI_COMPATIBLE",
        "api_base": "http://litellm:4000/v1",
        "api_key": "sk-test",
        "billing_tier": tier,
    }


@pytest.fixture
def shared(monkeypatch):
    from app.config import config as app_config

    connections, models = materialize_global_model_catalog(
        chat_configs=[
            _config(-1, "ds-deepseek-v4.1-flash", "standard"),
            _config(-3, "openai-gpt-6-luna", "premium"),
        ],
        image_configs=[],
    )
    monkeypatch.setattr(app_config, "GLOBAL_CONNECTIONS", connections)
    monkeypatch.setattr(app_config, "GLOBAL_MODELS", models)


async def _tiers(session) -> dict[str, tuple[str, str]]:
    connections = await list_global_connections(session=session, auth=None)
    return {
        m.model_id: (m.display_name, m.billing_tier)
        for conn in connections
        for m in conn.models
    }


async def test_without_an_admin_choice_the_yaml_tier_and_friendly_name_show(shared):
    got = await _tiers(_Settings([]))
    assert got == {
        "ds-deepseek-v4.1-flash": ("DeepSeek", "standard"),
        "openai-gpt-6-luna": ("Luna Pro", "premium"),
    }


async def test_the_admins_tier_wins(shared):
    row = BillingSetting(
        key="model_tiers",
        secret=False,
        value={"ds-deepseek-v4.1-flash": "premium", "openai-gpt-6-luna": "standard"},
    )
    got = await _tiers(_Settings([row]))
    assert got["ds-deepseek-v4.1-flash"][1] == "premium"
    assert got["openai-gpt-6-luna"][1] == "standard"
