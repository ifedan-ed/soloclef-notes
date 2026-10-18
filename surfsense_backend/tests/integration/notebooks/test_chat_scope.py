"""A notebook chat reads only its notebook's enabled sources.

Regression: a notebook chat cited the notebook's own Studio outputs (ARTIFACT
documents) and could reach any other workspace document, because
``search_knowledge_base`` widened past the sources and the ``/documents``
filesystem and workspace tree ignored them. Every path that searches, lists
or reads document content must now stay inside the scope.
"""

from __future__ import annotations

import contextlib
from types import SimpleNamespace

import pytest
from deepagents.backends.utils import create_file_data
from langchain.agents import create_agent
from langchain_core.messages import HumanMessage, ToolMessage
from langgraph.checkpoint.memory import InMemorySaver

import app.db as app_db
from app.agents.chat.document_scope import CONFIG_KEY
from app.agents.chat.multi_agent_chat.main_agent.middleware.knowledge_tree import (
    middleware as knowledge_tree_module,
)
from app.agents.chat.multi_agent_chat.main_agent.middleware.knowledge_tree.middleware import (
    KnowledgeTreeMiddleware,
)
from app.agents.chat.multi_agent_chat.shared.filesystem_selection import (
    FilesystemMode,
    FilesystemSelection,
)
from app.agents.chat.multi_agent_chat.shared.middleware.filesystem import (
    build_filesystem_mw,
)
from app.agents.chat.multi_agent_chat.shared.middleware.filesystem.backends import (
    kb_postgres,
)
from app.agents.chat.multi_agent_chat.shared.middleware.filesystem.backends.git_tree import (
    GitTreeBackend,
    build_scope_path_filter,
)
from app.agents.chat.multi_agent_chat.shared.middleware.filesystem.backends.kb_postgres import (
    KBPostgresBackend,
)
from app.agents.chat.multi_agent_chat.shared.middleware.filesystem.backends.resolver import (
    build_backend_resolver,
)
from app.agents.chat.multi_agent_chat.subagents.builtins.knowledge_base.tools import (
    search_knowledge_base,
)
from app.agents.chat.multi_agent_chat.subagents.builtins.knowledge_base.tools.search_knowledge_base import (
    create_search_knowledge_base_tool,
)
from app.agents.chat.shared.context import SurfSenseContextSchema
from app.config import config
from app.db import Chunk, Document, NewChatThread
from app.notebooks.chat.pins import load_notebook_pins
from tests.integration.harness import ScriptedTurn, build_scripted_harness

from .conftest import axis

pytestmark = pytest.mark.integration

QUIZ_PATH = "/documents/Quiz_ iron.md"


@pytest.fixture
def _db_reads_use_test_session(db_session, monkeypatch):
    @contextlib.asynccontextmanager
    async def _session():
        yield db_session

    for module in (search_knowledge_base, kb_postgres, knowledge_tree_module, app_db):
        monkeypatch.setattr(module, "shielded_async_session", _session)
    # Questions are embedded by the question model (app/embedding_roles).
    monkeypatch.setattr(
        config.query_embedding_model_instance, "embed", lambda _query: axis(0)
    )


@pytest.fixture
def notebook_world(
    db_session,
    make_owner,
    make_document,
    make_folder,
    make_notebook,
    make_studio_output,
    _db_reads_use_test_session,
):
    """A notebook with one source, its own Studio quiz, and other workspace docs."""

    async def build():
        owner = await make_owner()
        ws = owner.workspace.id
        papers = await make_folder(ws, name="Papers")
        private = await make_folder(ws, name="PrivateFolder")
        source = await make_document(
            ws, title="Source paper", text="iron source facts", folder_id=papers.id
        )
        outside = await make_document(
            ws, title="Outside paper", text="iron outside facts", folder_id=private.id
        )
        notebook = await make_notebook(ws, sources=[("document", source.id)])
        quiz = await make_studio_output(
            notebook, kind="quiz", artifact_format="quiz", title="Quiz: iron"
        )
        artifact_doc_id = quiz.artifact.document_id
        # Studio outputs live under /documents like any other document.
        quiz_doc = await db_session.get(Document, artifact_doc_id)
        quiz_doc.path = QUIZ_PATH
        db_session.add(
            Chunk(
                content="iron quiz facts",
                document_id=artifact_doc_id,
                position=0,
                embedding=axis(0),
            )
        )
        await db_session.flush()
        return SimpleNamespace(
            ws=ws,
            owner=owner,
            notebook=notebook,
            source=source,
            outside=outside,
            artifact_doc_id=artifact_doc_id,
            scope=[source.id],
        )

    return build


def _tool_runtime(*, scope=None, context=None, state=None):
    configurable = {} if scope is None else {CONFIG_KEY: list(scope)}
    return SimpleNamespace(
        state=state or {},
        tool_call_id="call-1",
        context=context,
        config={"configurable": configurable},
    )


async def _search(tool, runtime, top_k=5) -> str:
    result = await tool.coroutine("iron", runtime, top_k)
    return result if isinstance(result, str) else result.update["messages"][0].content


# ---------------------------------------------------------------- the scope


async def test_the_scope_is_enabled_documents_plus_folder_contents(
    db_session, make_owner, make_document, make_folder, make_notebook
):
    owner = await make_owner()
    ws = owner.workspace.id
    folder = await make_folder(ws, name="Guidelines")
    in_folder = await make_document(ws, title="In folder", folder_id=folder.id)
    loose = await make_document(ws, title="Loose")
    disabled = await make_document(ws, title="Disabled")
    await make_document(ws, title="Elsewhere")
    notebook = await make_notebook(
        ws,
        sources=[
            ("document", loose.id),
            ("document", disabled.id, False),
            ("folder", folder.id),
        ],
    )
    thread = NewChatThread(title="c", workspace_id=ws, notebook_id=notebook.id)
    db_session.add(thread)
    await db_session.flush()

    pins = await load_notebook_pins(db_session, chat_id=thread.id, workspace_id=ws)

    assert pins.scope_document_ids == sorted([loose.id, in_folder.id])


# ---------------------------------------------------------------- search


async def test_search_never_returns_the_notebooks_own_studio_outputs_or_other_docs(
    notebook_world,
):
    world = await notebook_world()
    tool = create_search_knowledge_base_tool(workspace_id=world.ws)

    # Subagent path: the scope arrives in the run config.
    rendered = await _search(tool, _tool_runtime(scope=world.scope))
    assert "iron source facts" in rendered
    assert "iron quiz facts" not in rendered
    assert "iron outside facts" not in rendered

    # Main-graph path: the scope arrives in runtime.context.
    ctx = SurfSenseContextSchema(workspace_id=world.ws, document_scope=world.scope)
    rendered = await _search(tool, _tool_runtime(context=ctx))
    assert "iron source facts" in rendered
    assert "iron quiz facts" not in rendered and "iron outside facts" not in rendered


async def test_a_plain_chat_still_searches_the_whole_knowledge_base(notebook_world):
    world = await notebook_world()
    tool = create_search_knowledge_base_tool(workspace_id=world.ws)

    rendered = await _search(tool, _tool_runtime())

    assert "iron source facts" in rendered
    assert "iron quiz facts" in rendered
    assert "iron outside facts" in rendered


async def test_a_mention_outside_the_notebook_does_not_widen_it(notebook_world):
    world = await notebook_world()
    tool = create_search_knowledge_base_tool(workspace_id=world.ws)
    runtime = _tool_runtime(
        scope=world.scope,
        state={"mentioned_document_ids": [world.outside.id]},
    )

    rendered = await _search(tool, runtime)

    assert "iron outside facts" not in rendered
    assert "iron source facts" in rendered


async def test_a_notebook_without_sources_finds_nothing(notebook_world):
    world = await notebook_world()
    tool = create_search_knowledge_base_tool(workspace_id=world.ws)

    rendered = await _search(tool, _tool_runtime(scope=[]))

    assert "no sources" in rendered
    assert "facts" not in rendered


# ---------------------------------------------------------------- filesystem


async def _paths(world) -> dict[str, str]:
    """Virtual paths of the world's documents, as an unscoped listing shows them."""
    backend = KBPostgresBackend(world.ws, _tool_runtime())
    listing = await backend.alist_tree_listing("/documents")
    files = [e["path"] for e in listing["entries"] if not e["is_dir"]]

    def find(fragment: str) -> str:
        return next(p for p in files if fragment in p)

    return {
        "source": find("Source paper"),
        "outside": find("Outside paper"),
        "quiz": QUIZ_PATH,
    }


async def test_the_filesystem_lists_and_reads_only_notebook_sources(notebook_world):
    world = await notebook_world()
    paths = await _paths(world)
    backend = KBPostgresBackend(world.ws, _tool_runtime(scope=world.scope))

    root = [info["path"] for info in await backend.als_info("/documents")]
    assert "/documents/Papers" in root
    assert "/documents/PrivateFolder" not in root
    assert not any("Quiz" in path for path in root)

    assert [i["path"] for i in await backend.als_info("/documents/PrivateFolder")] == []

    tree = await backend.alist_tree_listing("/documents")
    tree_paths = {entry["path"] for entry in tree["entries"]}
    assert paths["source"] in tree_paths
    assert paths["outside"] not in tree_paths
    assert not any("Quiz" in path for path in tree_paths)
    assert "/documents/PrivateFolder" not in tree_paths

    globbed = {info["path"] for info in await backend.aglob_info("**", "/documents")}
    assert globbed == {paths["source"]}

    grepped = await backend.agrep_raw("iron", "/documents")
    assert {match["path"] for match in grepped} == {paths["source"]}

    assert await backend.aload_document(paths["source"]) is not None
    assert await backend.aload_document(paths["outside"]) is None
    assert await backend.aload_document(paths["quiz"]) is None
    assert "not found" in await backend.aread(paths["quiz"])


async def test_the_filesystem_is_unchanged_for_plain_chats(notebook_world):
    world = await notebook_world()
    paths = await _paths(world)
    backend = KBPostgresBackend(world.ws, _tool_runtime())

    assert await backend.aload_document(paths["outside"]) is not None
    assert await backend.aload_document(paths["quiz"]) is not None
    root = [info["path"] for info in await backend.als_info("/documents")]
    assert "/documents/PrivateFolder" in root


def _read_turns(*paths: str) -> list[ScriptedTurn]:
    return [
        ScriptedTurn(
            tool_calls=[
                {"name": "read_file", "args": {"file_path": path}, "id": f"r{i}"}
                for i, path in enumerate(paths)
            ]
        ),
        ScriptedTurn(text="done"),
    ]


async def _run_read_file(world, turns, *, scope, initial_state=None):
    selection = FilesystemSelection(mode=FilesystemMode.CLOUD)
    fs_mw = build_filesystem_mw(
        backend_resolver=build_backend_resolver(selection, workspace_id=world.ws),
        filesystem_mode=FilesystemMode.CLOUD,
        workspace_id=world.ws,
        user_id=str(world.owner.user.id),
        thread_id=1,
        read_only=True,
    )
    harness = build_scripted_harness(turns=turns)
    agent = create_agent(
        harness.model, tools=[], middleware=[fs_mw], checkpointer=InMemorySaver()
    )
    configurable = {"thread_id": f"scope-{world.ws}-{id(turns)}"}
    if scope is not None:
        configurable[CONFIG_KEY] = scope
    result = await agent.ainvoke(
        {"messages": [HumanMessage(content="read")], **(initial_state or {})},
        config={"configurable": configurable},
    )
    return {
        m.tool_call_id: str(m.content)
        for m in result["messages"]
        if isinstance(m, ToolMessage)
    }


async def test_read_file_through_a_real_agent_graph_honours_the_scope(notebook_world):
    """The run-config channel reaches the tool the way a subagent sees it."""
    world = await notebook_world()
    paths = await _paths(world)

    replies = await _run_read_file(
        world,
        _read_turns(paths["source"], paths["quiz"], paths["outside"]),
        scope=world.scope,
    )

    assert "iron source facts" in replies["r0"]
    assert "not found" in replies["r1"] and "iron quiz" not in replies["r1"]
    assert "not found" in replies["r2"] and "iron outside" not in replies["r2"]


async def test_a_document_cached_by_an_earlier_read_stays_hidden(notebook_world):
    world = await notebook_world()
    paths = await _paths(world)
    cached = {
        "files": {paths["quiz"]: create_file_data("iron quiz facts (cached)")},
        "doc_id_by_path": {paths["quiz"]: world.artifact_doc_id},
    }

    replies = await _run_read_file(
        world, _read_turns(paths["quiz"]), scope=world.scope, initial_state=cached
    )

    assert "not found" in replies["r0"] and "cached" not in replies["r0"]


# ---------------------------------------------------------------- workspace tree


async def test_the_workspace_tree_shows_only_notebook_sources(notebook_world):
    world = await notebook_world()
    middleware = KnowledgeTreeMiddleware(
        workspace_id=world.ws, filesystem_mode=FilesystemMode.CLOUD
    )
    state = {"messages": [HumanMessage(content="hi")]}

    scoped = await middleware.abefore_agent(
        state,
        SimpleNamespace(
            context=SurfSenseContextSchema(document_scope=world.scope), config=None
        ),
    )
    tree = scoped["workspace_tree_text"]
    assert "Source paper" in tree and "Papers/" in tree
    assert "Outside paper" not in tree
    assert "PrivateFolder" not in tree
    assert "Quiz" not in tree

    # The cache is per scope: a plain chat in the same workspace sees everything.
    plain = await middleware.abefore_agent(
        state, SimpleNamespace(context=SurfSenseContextSchema(), config=None)
    )
    assert "Outside paper" in plain["workspace_tree_text"]
    assert "Quiz" in plain["workspace_tree_text"]


# ---------------------------------------------------------------- git-native backend


class _FakeWorkingCopy:
    def __init__(self, paths: dict[str, str]):
        self.paths = paths

    async def als_info(self, path):
        return [{"path": p, "is_dir": False} for p in self.paths] + [
            {"path": "/documents/PrivateFolder", "is_dir": True},
            {"path": "/documents/Papers", "is_dir": True},
        ]

    async def aread(self, file_path, offset=0, limit=2000):
        return self.paths.get(file_path, "Error: missing")

    async def agrep_raw(self, pattern, path=None, glob=None):
        return [{"path": p, "line": 1, "text": t} for p, t in self.paths.items()]


async def test_the_git_native_backend_filters_by_path(notebook_world):
    world = await notebook_world()
    paths = await _paths(world)

    path_filter = await build_scope_path_filter(world.ws, frozenset(world.scope))
    assert path_filter.hides_file(paths["outside"])
    assert path_filter.hides_file(paths["quiz"])
    assert not path_filter.hides_file(paths["source"])
    assert not path_filter.hides_file("/documents/Papers/new-note.md")
    assert path_filter.hides_entry("/documents/PrivateFolder", is_dir=True)
    assert not path_filter.hides_entry("/documents/Papers", is_dir=True)

    backend = GitTreeBackend(world.ws, _tool_runtime(scope=world.scope))
    backend._mounted = _FakeWorkingCopy(
        {
            paths["source"]: "iron source facts",
            paths["outside"]: "iron outside facts",
            paths["quiz"]: "iron quiz facts",
        }
    )
    listed = {info["path"] for info in await backend.als_info("/documents")}
    assert listed == {paths["source"], "/documents/Papers"}
    assert "not found" in await backend.aread(paths["outside"])
    assert await backend.aread(paths["source"]) == "iron source facts"
    grepped = await backend.agrep_raw("iron")
    assert {m["path"] for m in grepped} == {paths["source"]}


# ---------------------------------------------------------------- a dropped file
#
# Regression (notebook 10 on notes.example.com): the notebook's only source was
# a slide deck dragged onto the notebook, i.e. a root-level FILE document linked
# through the sources route. The owner's composer retrieval scope was "web" (a
# workspace-wide preference), so the notebook chat's knowledge-base tools were
# all denied and its workspace tree was empty; the agent's globs by the deck's
# name ("**/*.pptx") also missed, because documents are served as ``.xml``.

DECK = "complications-of-premature-delivery.pptx"
DECK_PATH = f"/documents/{DECK}.xml"


@pytest.fixture
def dropped_deck_world(
    db_session,
    make_owner,
    make_document,
    make_notebook,
    make_studio_output,
    _db_reads_use_test_session,
):
    """A notebook whose only source is a dropped root-level deck, plus decoys."""
    from app.db import DocumentType
    from app.notebooks.routes import sources as sources_routes
    from app.notebooks.schemas import SourcesAdd, SourceTarget

    async def build():
        owner = await make_owner()
        ws = owner.workspace.id
        notebook = await make_notebook(ws, title="Untitled notebook")
        # What the upload endpoint leaves behind for a dropped file: a FILE
        # document at the workspace root, no path column, several chunks.
        deck = Document(
            title=DECK,
            document_type=DocumentType.FILE,
            document_metadata={"FILE_NAME": DECK, "ETL_SERVICE": "LLAMACLOUD"},
            content="Complications of Premature Delivery",
            source_markdown="Complications of Premature Delivery",
            content_hash="deck-hash",
            workspace_id=ws,
            folder_id=None,
            status={"state": "ready"},
        )
        db_session.add(deck)
        await db_session.flush()
        for position, text in enumerate(
            [
                "Respiratory distress syndrome is caused by surfactant deficiency.",
                "Bronchopulmonary dysplasia follows prolonged ventilation.",
                "Apnea of prematurity needs caffeine.",
            ]
        ):
            db_session.add(
                Chunk(
                    content=text,
                    document_id=deck.id,
                    position=position,
                    embedding=axis(0),
                )
            )
        # Decoys: another root-level deck outside the notebook, and another
        # notebook's Studio output about the same topic.
        outside = await make_document(
            ws,
            title="Other lecture.pptx",
            text="Respiratory distress outside the notebook.",
        )
        other_notebook = await make_notebook(ws, title="Other notebook")
        studio = await make_studio_output(
            other_notebook, kind="quiz", artifact_format="quiz", title="Quiz: lungs"
        )
        db_session.add(
            Chunk(
                content="Respiratory distress studio quiz.",
                document_id=studio.artifact.document_id,
                position=0,
                embedding=axis(0),
            )
        )
        await db_session.flush()

        # Linked the way a drop links it: POST .../notebooks/{id}/sources.
        await sources_routes.add_sources(
            ws,
            notebook.id,
            SourcesAdd(targets=[SourceTarget(kind="document", id=deck.id)]),
            db_session,
            owner.auth,
        )
        thread = NewChatThread(
            title="Respiratory problems", workspace_id=ws, notebook_id=notebook.id
        )
        db_session.add(thread)
        await db_session.flush()
        pins = await load_notebook_pins(db_session, chat_id=thread.id, workspace_id=ws)
        return SimpleNamespace(
            ws=ws,
            owner=owner,
            deck=deck,
            outside=outside,
            studio_doc_id=studio.artifact.document_id,
            pins=pins,
            scope=pins.scope_document_ids,
        )

    return build


async def test_a_dropped_root_level_file_is_the_notebooks_scope(dropped_deck_world):
    world = await dropped_deck_world()

    assert world.pins.document_ids == [world.deck.id]
    assert world.scope == [world.deck.id]
    assert world.pins.source_titles == [DECK]


@pytest.mark.parametrize("requested", ["documents", "web", "all"])
async def test_a_notebook_chat_is_sources_only_under_any_requested_scope(
    dropped_deck_world, requested
):
    from app.agents.chat.retrieval_scope import (
        WEB_RETRIEVAL_SUBAGENTS,
        RetrievalScope,
        SourcesOnly,
        denied_retrieval_tools,
        excluded_retrieval_subagents,
    )
    from app.notebooks.chat.pins import notebook_retrieval_scope

    world = await dropped_deck_world()

    # The old composer switch survives as a workspace cookie; whatever it
    # says, a notebook turn reads its own sources and nothing from the web.
    scope, sources_only = notebook_retrieval_scope(
        RetrievalScope(requested), world.pins
    )
    assert (scope, sources_only) == (RetrievalScope.DOCUMENTS, SourcesOnly())
    assert (
        denied_retrieval_tools(scope, subagent_name="knowledge_base", tools=[]) == set()
    )
    assert excluded_retrieval_subagents(scope, sources_only) == set(
        WEB_RETRIEVAL_SUBAGENTS
    )
    # A plain chat keeps what it asked for.
    assert notebook_retrieval_scope(RetrievalScope(requested), None) == (
        RetrievalScope(requested),
        None,
    )


async def test_search_finds_the_dropped_deck_and_nothing_else(dropped_deck_world):
    world = await dropped_deck_world()
    tool = create_search_knowledge_base_tool(workspace_id=world.ws)

    for runtime in (
        _tool_runtime(scope=world.scope),
        _tool_runtime(
            context=SurfSenseContextSchema(
                workspace_id=world.ws, document_scope=world.scope
            )
        ),
    ):
        result = await tool.coroutine("respiratory distress", runtime, 10)
        rendered = (
            result if isinstance(result, str) else result.update["messages"][0].content
        )
        assert "surfactant deficiency" in rendered
        assert "outside the notebook" not in rendered
        assert "studio quiz" not in rendered


async def test_the_filesystem_finds_the_dropped_deck_by_its_own_name(
    dropped_deck_world,
):
    world = await dropped_deck_world()
    backend = KBPostgresBackend(world.ws, _tool_runtime(scope=world.scope))

    # The globs the agent actually ran in the failing chat.
    for pattern in (f"**/{DECK}", "**/*.pptx", f"**/{DECK}.xml", "**"):
        globbed = [info["path"] for info in await backend.aglob_info(pattern, "/")]
        assert globbed == [DECK_PATH], pattern

    listed = [info["path"] for info in await backend.als_info("/documents")]
    assert listed == [DECK_PATH]
    tree = await backend.alist_tree_listing("/documents")
    assert [e["path"] for e in tree["entries"] if not e["is_dir"]] == [DECK_PATH]

    grepped = await backend.agrep_raw("Respiratory", "/documents", "*.pptx")
    assert {match["path"] for match in grepped} == {DECK_PATH}

    # Read by the served path and by the name the user knows.
    for path in (DECK_PATH, f"/documents/{DECK}"):
        loaded = await backend.aload_document(path)
        assert loaded is not None and loaded[1] == world.deck.id, path

    # The decoys stay out of every view.
    unscoped = KBPostgresBackend(world.ws, _tool_runtime())
    everything = {info["path"] for info in await unscoped.aglob_info("**/*.pptx", "/")}
    assert DECK_PATH in everything and len(everything) == 2


async def test_read_file_of_the_dropped_deck_through_a_real_agent_graph(
    dropped_deck_world,
):
    world = await dropped_deck_world()

    replies = await _run_read_file(
        world, _read_turns(DECK_PATH, f"/documents/{DECK}"), scope=world.scope
    )

    assert "surfactant deficiency" in replies["r0"]
    assert "surfactant deficiency" in replies["r1"]


async def test_the_workspace_tree_shows_the_dropped_deck(dropped_deck_world):
    world = await dropped_deck_world()
    middleware = KnowledgeTreeMiddleware(
        workspace_id=world.ws, filesystem_mode=FilesystemMode.CLOUD
    )

    update = await middleware.abefore_agent(
        {"messages": [HumanMessage(content="hi")]},
        SimpleNamespace(
            context=SurfSenseContextSchema(document_scope=world.scope), config=None
        ),
    )
    tree = update["workspace_tree_text"]
    assert f"{DECK}.xml" in tree
    assert "Other lecture" not in tree
    assert "Quiz" not in tree
