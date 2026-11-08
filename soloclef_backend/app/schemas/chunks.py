from pydantic import BaseModel, ConfigDict

from .base import IDModel, TimestampModel


class ChunkBase(BaseModel):
    content: str
    document_id: int


class ChunkCreate(ChunkBase):
    pass


class ChunkUpdate(ChunkBase):
    pass


class ChunkRead(ChunkBase, IDModel, TimestampModel):
    #: 1-based inclusive pages of the original PDF this chunk was read from;
    #: None when unknown (not a PDF, or not located yet).
    page_start: int | None = None
    page_end: int | None = None

    model_config = ConfigDict(from_attributes=True)
