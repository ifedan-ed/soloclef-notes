"""deepagents adapter: agent file ops on the turn's private working copy."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from deepagents.backends.protocol import (
    EditResult,
    FileDownloadResponse,
    FileInfo,
    FileUploadResponse,
    GrepMatch,
    WriteResult,
)
from langgraph.prebuilt.tool_node import ToolRuntime

from app.agents.chat.document_scope import DocumentScope, document_scope_of
from app.agents.chat.multi_agent_chat.shared.middleware.filesystem.backends.multi_root_local_folder import (
    MultiRootLocalFolderBackend,
)
from app.knowledge_store import KnowledgeStore
from app.knowledge_store.service import thread_working_copy_id

__all__ = [
    "GitTreeBackend",
    "ScopePathFilter",
    "build_scope_path_filter",
    "thread_working_copy_id",
]

_DOCUMENTS_MOUNT = "documents"


@dataclass(frozen=True)
class ScopePathFilter:
    """Which ``/documents`` paths a scoped turn must not see.

    ``hidden_files`` are the paths of workspace documents outside the scope;
    ``hidden_dirs`` are folders that lead to no in-scope document. A path with
    no document row (a file the agent wrote itself) is never hidden.
    """

    hidden_files: frozenset[str]
    hidden_dirs: frozenset[str]

    def hides_file(self, path: str | None) -> bool:
        return bool(path) and path.rstrip("/") in self.hidden_files

    def hides_entry(self, path: str | None, *, is_dir: bool = False) -> bool:
        if not path:
            return False
        clean = path.rstrip("/")
        return clean in self.hidden_files or (is_dir and clean in self.hidden_dirs)


async def build_scope_path_filter(
    workspace_id: int, scope: frozenset[int]
) -> ScopePathFilter:
    """Map a document-id scope onto the working copy's ``/documents`` paths."""
    from sqlalchemy import select

    from app.db import Document, shielded_async_session
    from app.knowledge_store.paths import (
        DOCUMENTS_ROOT,
        build_path_index,
        doc_to_virtual_path,
        recorded_virtual_path,
    )

    async with shielded_async_session() as session:
        index = await build_path_index(session, int(workspace_id))
        rows = (
            await session.execute(
                select(
                    Document.id,
                    Document.title,
                    Document.folder_id,
                    Document.path,
                    Document.document_metadata,
                ).where(Document.workspace_id == int(workspace_id))
            )
        ).all()

    hidden: set[str] = set()
    allowed: set[str] = set()
    for row in rows:
        path = recorded_virtual_path(row.document_metadata, row.path) or (
            doc_to_virtual_path(
                doc_id=row.id,
                title=str(row.title or "untitled"),
                folder_id=row.folder_id,
                index=index,
            )
        )
        (allowed if row.id in scope else hidden).add(path)
    hidden -= allowed

    leading: set[str] = set()
    for path in allowed:
        parent = path.rsplit("/", 1)[0]
        while parent.startswith(DOCUMENTS_ROOT + "/"):
            leading.add(parent)
            parent = parent.rsplit("/", 1)[0]
    folders = set(index.folder_paths.values())
    for path in hidden:
        parent = path.rsplit("/", 1)[0]
        while parent.startswith(DOCUMENTS_ROOT + "/"):
            folders.add(parent)
            parent = parent.rsplit("/", 1)[0]
    return ScopePathFilter(
        hidden_files=frozenset(hidden),
        hidden_dirs=frozenset(folders - leading),
    )


class GitTreeBackend:
    """Serve ``/documents/...`` from the turn's private working copy.

    A thin mount over the knowledge store's working copy: opened lazily on the
    first operation, plain file ops for the rest of the turn. No staging, no
    state overlay; the end-of-turn commit reads the copy's diff.
    """

    def __init__(self, workspace_id: int | str, runtime: ToolRuntime) -> None:
        self.workspace_id = workspace_id
        self._runtime = runtime
        self._mounted: MultiRootLocalFolderBackend | None = None
        self.scope: DocumentScope = document_scope_of(runtime)
        self._filter: ScopePathFilter | None = None

    async def _scope_filter(self) -> ScopePathFilter | None:
        """The turn's path filter, or ``None`` for an unscoped turn."""
        if self.scope is None:
            return None
        if self._filter is None:
            self._filter = await build_scope_path_filter(
                int(self.workspace_id), self.scope
            )
        return self._filter

    async def _hidden(self, *paths: str) -> bool:
        path_filter = await self._scope_filter()
        return path_filter is not None and any(
            path_filter.hides_file(path) for path in paths
        )

    async def _visible_infos(self, infos: list[FileInfo]) -> list[FileInfo]:
        path_filter = await self._scope_filter()
        if path_filter is None:
            return infos
        return [
            info
            for info in infos
            if not path_filter.hides_entry(
                info.get("path"), is_dir=bool(info.get("is_dir"))
            )
        ]

    async def _backend(self) -> MultiRootLocalFolderBackend:
        if self._mounted is None:
            configurable = (self._runtime.config or {}).get("configurable") or {}
            store = KnowledgeStore.for_workspace(self.workspace_id)
            copy = await store.open_turn_copy(configurable.get("thread_id"))
            # Mount the copy's documents/ subtree, not its root: the repo keeps
            # the documents/ prefix (C1), so agent writes must land under it —
            # the same paths the editor recorder and the migration seeder use.
            documents_root = copy.path / _DOCUMENTS_MOUNT
            documents_root.mkdir(exist_ok=True)
            self._mounted = MultiRootLocalFolderBackend(
                ((_DOCUMENTS_MOUNT, str(documents_root)),)
            )
        return self._mounted

    async def als_info(self, path: str) -> list[FileInfo]:
        return await self._visible_infos(await (await self._backend()).als_info(path))

    async def aread(self, file_path: str, offset: int = 0, limit: int = 2000) -> str:
        if await self._hidden(file_path):
            return _not_found(file_path)
        return await (await self._backend()).aread(file_path, offset, limit)

    async def aread_raw(self, file_path: str) -> str:
        if await self._hidden(file_path):
            return _not_found(file_path)
        return await (await self._backend()).aread_raw(file_path)

    async def awrite(self, file_path: str, content: str) -> WriteResult:
        return await (await self._backend()).awrite(file_path, content)

    async def aedit(
        self,
        file_path: str,
        old_string: str,
        new_string: str,
        replace_all: bool = False,
    ) -> EditResult:
        if await self._hidden(file_path):
            return EditResult(error=_not_found(file_path))
        return await (await self._backend()).aedit(
            file_path, old_string, new_string, replace_all
        )

    async def aglob_info(self, pattern: str, path: str = "/") -> list[FileInfo]:
        return await self._visible_infos(
            await (await self._backend()).aglob_info(pattern, path)
        )

    async def agrep_raw(
        self,
        pattern: str,
        path: str | None = None,
        glob: str | None = None,
    ) -> list[GrepMatch] | str:
        matches = await (await self._backend()).agrep_raw(pattern, path, glob)
        path_filter = await self._scope_filter()
        if path_filter is None or isinstance(matches, str):
            return matches
        return [m for m in matches if not path_filter.hides_file(m.get("path"))]

    async def alist_tree(
        self,
        path: str = "/",
        *,
        max_depth: int | None = 8,
        page_size: int = 500,
        include_files: bool = True,
        include_dirs: bool = True,
    ) -> dict[str, Any]:
        listing = await (await self._backend()).alist_tree(
            path,
            max_depth=max_depth,
            page_size=page_size,
            include_files=include_files,
            include_dirs=include_dirs,
        )
        path_filter = await self._scope_filter()
        if path_filter is None or not isinstance(listing.get("entries"), list):
            return listing
        return {
            **listing,
            "entries": [
                entry
                for entry in listing["entries"]
                if not path_filter.hides_entry(
                    entry.get("path"), is_dir=bool(entry.get("is_dir"))
                )
            ],
        }

    async def amove(
        self,
        source_path: str,
        destination_path: str,
        overwrite: bool = False,
    ) -> WriteResult:
        if await self._hidden(source_path, destination_path):
            return WriteResult(error=_not_found(source_path))
        return await (await self._backend()).amove(
            source_path, destination_path, overwrite
        )

    async def adelete_file(self, file_path: str) -> WriteResult:
        if await self._hidden(file_path):
            return WriteResult(error=_not_found(file_path))
        return await (await self._backend()).adelete_file(file_path)

    async def amkdir(
        self,
        dir_path: str,
        parents: bool = True,
        exist_ok: bool = True,
    ) -> WriteResult:
        # Git ignores empty directories, so an empty folder needs a .keep marker
        # to enter the turn's diff — the marker the facade's create_folder writes.
        # The dir must exist first for that marker write to resolve.
        backend = await self._backend()
        made = await backend.amkdir(dir_path, parents=parents, exist_ok=exist_ok)
        if made.error:
            return made
        return await backend.awrite(self._keep_marker(dir_path), "")

    async def armdir(self, dir_path: str) -> WriteResult:
        # A folder is removed by removing its .keep; refuse one that still holds
        # documents. An untracked folder has no marker, so a missing one succeeds.
        backend = await self._backend()
        keep = self._keep_marker(dir_path)
        for info in await backend.als_info(dir_path):
            path = (info.get("path") or "").rstrip("/")
            if path and path != keep:
                return WriteResult(
                    error=f"Error: directory '{dir_path}' is not empty. "
                    "Remove its contents first."
                )
        result = await backend.adelete_file(keep)
        return WriteResult(path=dir_path) if result.error else result

    @staticmethod
    def _keep_marker(dir_path: str) -> str:
        from app.knowledge_store.paths import KEEP_FILE

        return f"{dir_path.rstrip('/')}/{KEEP_FILE}"

    async def aupload_files(
        self, files: list[tuple[str, bytes]]
    ) -> list[FileUploadResponse]:
        return await (await self._backend()).aupload_files(files)

    async def adownload_files(self, paths: list[str]) -> list[FileDownloadResponse]:
        path_filter = await self._scope_filter()
        if path_filter is None:
            return await (await self._backend()).adownload_files(paths)
        visible = [p for p in paths if not path_filter.hides_file(p)]
        fetched = {
            r.path: r
            for r in (
                await (await self._backend()).adownload_files(visible)
                if visible
                else []
            )
        }
        return [
            fetched.get(p)
            or FileDownloadResponse(path=p, content=None, error="file_not_found")
            for p in paths
        ]


def _not_found(path: str) -> str:
    return f"Error: File '{path}' not found"
