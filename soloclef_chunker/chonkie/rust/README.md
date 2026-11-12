# The static library the Go chunker links against

`../chonkie.go` is cgo over this crate, and this crate is a thin C ABI over
two libraries the Python side already runs:

| Crate | What it is |
|-------|------------|
| `chunk` | chonkie's own core, pinned with `=` to the version the installed `chonkie-core` wraps. It does the delimiter splitting and the merge back up to the chunk ceiling. |
| `tokenizers` | HuggingFace's, reading the same `tokenizer.json` the backend image bakes. It does the token counting and the per-token offsets the last chunking level cuts at. |

Nothing here reimplements either. That is the whole point: the Go chunker has
to cut where the Python chunker cuts, and the surest way to cut in the same
place is to run the same code.

## Building it

`go build`, `go vet` and `go test` in `soloclef_chunker` all link this, so it
has to exist before any of them:

```bash
cargo build --release --manifest-path chonkie/rust/Cargo.toml
```

That writes `target/release/libsoloclef_chonkie.a`, which `../chonkie.go`
names in its `#cgo LDFLAGS`. `target/` is gitignored build output.

It is a `staticlib`, not a `cdylib`, so a Go binary built against it needs no
shared object beside it at run time — deployment stays the single file it is
today.

## Before changing the pinned versions

`chunk 0.9.2` is the core `chonkie-core 0.9.2` wrapped, which is what cut the
chunks the fixtures hold. A newer core may merge in a different place, and a
boundary that moves is a stored chunk that no longer matches the vector built
from it, so the pin is `=` and changing it means the fixture tests in
`../../tablesafe` and `../../recursive` must still pass byte for byte, and a
reindex argument of its own.

`CoreVersion()` on the Go side returns the pinned version so the two can be
compared without reading this file.

## Conventions across the boundary

* Text goes in as a pointer and a byte length, never assumed NUL-terminated,
  and is validated as UTF-8. Invalid bytes are an error, not a silent
  replacement: a replacement character would change a token count.
* Every function that can fail returns a negative `isize` or null and writes
  an owned C string to `*err`, which the caller frees with `sc_string_free`.
* The crate is built with `panic = "abort"`, so a panic cannot unwind into Go;
  each entry point catches what it can first.
* A list of strings comes back as one allocation: little-endian `u32` count,
  then that many `u32` byte lengths, then the bytes end to end. Freed with
  `sc_buffer_free`. Offset arrays come back as `usize` arrays freed with
  `sc_offsets_free`.
