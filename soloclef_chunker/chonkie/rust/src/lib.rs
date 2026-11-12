//! A C ABI over chonkie's Rust core and HuggingFace's tokenizers, for Go.
//!
//! Why this exists
//! ---------------
//! The Go chunker reproduces chonkie's `RecursiveChunker` cut for cut, because
//! the boundaries it decides are already in the database against the chunks
//! and vectors they produced. Reproducing a tokenizer is not something to
//! attempt by resemblance: BGE-M3 is XLM-RoBERTa Unigram with a precompiled
//! character map, and an implementation that is right on ninety-nine documents
//! and wrong on the hundredth is worse than none, because the hundredth is
//! silently misindexed.
//!
//! So the Go side calls the same code Python does -- the same `chunk` crate
//! version chonkie-core wraps, and the same `tokenizers` library, reading the
//! same `tokenizer.json` the image bakes. Agreement is then a property of the
//! build, not a thing to test for and hope.
//!
//! Verified before this was written: on a 246,503-character document from the
//! live corpus, the token count (61,659), all 121 decoded 512-token pieces and
//! all 137 `merge_splits` outputs were byte-for-byte identical to Python's.
//!
//! Conventions across the boundary
//! -------------------------------
//! * Text goes in as a pointer and a byte length. It is never assumed to be
//!   NUL-terminated and is validated as UTF-8; invalid bytes are an error, not
//!   a silent replacement, because a replacement character would change a
//!   token count.
//! * Every function that can fail returns a negative `isize`, or null, and
//!   writes an owned C string to `*err` for the caller to free with
//!   `sc_string_free`. Nothing panics across the boundary: the crate is built
//!   with `panic = "abort"`, so a panic would take the process down rather
//!   than unwind into Go, and each entry point catches what it can first.
//! * A list of strings comes back as one buffer in a single allocation, laid
//!   out as little-endian `u32` count, then `count` `u32` byte lengths, then
//!   the bytes end to end. One allocation and one free per call, and Go can
//!   slice it without copying. `sc_buffer_free` returns it.

use std::ffi::{c_char, c_void, CString};
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::ptr;
use std::slice;

use chunk::IncludeDelim;
use tokenizers::Tokenizer;

/// A loaded tokenizer, handed to Go as an opaque pointer.
pub struct Handle {
    tokenizer: Tokenizer,
}

/// Writes `message` to `*err` as an owned C string, when `err` is not null.
unsafe fn set_err(err: *mut *mut c_char, message: impl AsRef<str>) {
    if err.is_null() {
        return;
    }
    let owned = CString::new(message.as_ref().replace('\0', " "))
        .unwrap_or_else(|_| CString::new("error message contained a NUL").unwrap());
    *err = owned.into_raw();
}

/// The text at `ptr`/`len` as `&str`, or `None` when it is not valid UTF-8.
unsafe fn as_str<'a>(ptr: *const u8, len: usize) -> Option<&'a str> {
    if ptr.is_null() {
        return if len == 0 { Some("") } else { None };
    }
    std::str::from_utf8(slice::from_raw_parts(ptr, len)).ok()
}

/// Packs `items` into the single-allocation layout described at the top.
fn pack(items: &[&str]) -> Vec<u8> {
    let total: usize = items.iter().map(|s| s.len()).sum();
    let mut out = Vec::with_capacity(4 + 4 * items.len() + total);
    out.extend_from_slice(&(items.len() as u32).to_le_bytes());
    for item in items {
        out.extend_from_slice(&(item.len() as u32).to_le_bytes());
    }
    for item in items {
        out.extend_from_slice(item.as_bytes());
    }
    out
}

/// Hands a packed buffer to Go: pointer and length, ownership transferred.
unsafe fn give(buffer: Vec<u8>, out_len: *mut usize) -> *mut u8 {
    let mut boxed = buffer.into_boxed_slice();
    let ptr = boxed.as_mut_ptr();
    let len = boxed.len();
    std::mem::forget(boxed);
    if !out_len.is_null() {
        *out_len = len;
    }
    ptr
}

/// Loads `tokenizer.json` from `path` (a NUL-terminated C string).
///
/// Returns null on failure, with the reason in `*err`.
#[no_mangle]
pub unsafe extern "C" fn sc_tokenizer_open(
    path: *const c_char,
    err: *mut *mut c_char,
) -> *mut c_void {
    if path.is_null() {
        set_err(err, "sc_tokenizer_open: path is null");
        return ptr::null_mut();
    }
    let path = match std::ffi::CStr::from_ptr(path).to_str() {
        Ok(p) => p.to_owned(),
        Err(e) => {
            set_err(err, format!("sc_tokenizer_open: path is not UTF-8: {e}"));
            return ptr::null_mut();
        }
    };
    match catch_unwind(|| Tokenizer::from_file(&path)) {
        Ok(Ok(tokenizer)) => Box::into_raw(Box::new(Handle { tokenizer })) as *mut c_void,
        Ok(Err(e)) => {
            set_err(err, format!("sc_tokenizer_open: {path}: {e}"));
            ptr::null_mut()
        }
        Err(_) => {
            set_err(err, format!("sc_tokenizer_open: {path}: panicked"));
            ptr::null_mut()
        }
    }
}

/// Frees a tokenizer from `sc_tokenizer_open`. Null is a no-op.
#[no_mangle]
pub unsafe extern "C" fn sc_tokenizer_close(handle: *mut c_void) {
    if !handle.is_null() {
        drop(Box::from_raw(handle as *mut Handle));
    }
}

/// Frees a C string this library wrote to an `err` out-parameter.
#[no_mangle]
pub unsafe extern "C" fn sc_string_free(s: *mut c_char) {
    if !s.is_null() {
        drop(CString::from_raw(s));
    }
}

/// Frees a packed buffer this library returned.
#[no_mangle]
pub unsafe extern "C" fn sc_buffer_free(ptr: *mut u8, len: usize) {
    if !ptr.is_null() && len > 0 {
        drop(Vec::from_raw_parts(ptr, len, len));
    }
}

/// How many tokens `text` holds, or a negative value on failure.
///
/// Special tokens are not added: chonkie counts the text's own tokens, and a
/// `<s>`/`</s>` pair would make every count two too high.
#[no_mangle]
pub unsafe extern "C" fn sc_count(
    handle: *mut c_void,
    text: *const u8,
    len: usize,
    err: *mut *mut c_char,
) -> isize {
    let handle = match (handle as *mut Handle).as_ref() {
        Some(h) => h,
        None => {
            set_err(err, "sc_count: tokenizer handle is null");
            return -1;
        }
    };
    let text = match as_str(text, len) {
        Some(t) => t,
        None => {
            set_err(err, "sc_count: text is not valid UTF-8");
            return -2;
        }
    };
    match catch_unwind(AssertUnwindSafe(|| handle.tokenizer.encode(text, false))) {
        Ok(Ok(encoding)) => encoding.get_ids().len() as isize,
        Ok(Err(e)) => {
            set_err(err, format!("sc_count: {e}"));
            -3
        }
        Err(_) => {
            set_err(err, "sc_count: panicked");
            -4
        }
    }
}

/// Encodes `text`, cuts the token sequence into runs of `size`, and returns
/// the decoded runs packed into one buffer.
///
/// This is chonkie's last recursion level: with no delimiter left to cut at,
/// it slices the ids every `chunk_size` and decodes each slice. Decoding is
/// what makes this a tokenizer call rather than arithmetic -- the text that
/// comes back is the tokenizer's rendering of those ids, which for a
/// SentencePiece vocabulary is not the same as the input sliced by characters.
#[no_mangle]
pub unsafe extern "C" fn sc_split_tokens(
    handle: *mut c_void,
    text: *const u8,
    len: usize,
    size: usize,
    out_len: *mut usize,
    err: *mut *mut c_char,
) -> *mut u8 {
    let handle = match (handle as *mut Handle).as_ref() {
        Some(h) => h,
        None => {
            set_err(err, "sc_split_tokens: tokenizer handle is null");
            return ptr::null_mut();
        }
    };
    let text = match as_str(text, len) {
        Some(t) => t,
        None => {
            set_err(err, "sc_split_tokens: text is not valid UTF-8");
            return ptr::null_mut();
        }
    };
    if size == 0 {
        set_err(err, "sc_split_tokens: size must be at least 1");
        return ptr::null_mut();
    }
    let result = catch_unwind(AssertUnwindSafe(|| -> Result<Vec<u8>, String> {
        let encoding = handle
            .tokenizer
            .encode(text, false)
            .map_err(|e| e.to_string())?;
        let ids = encoding.get_ids();
        let mut pieces: Vec<String> = Vec::new();
        let mut i = 0;
        while i < ids.len() {
            let end = (i + size).min(ids.len());
            pieces.push(
                handle
                    .tokenizer
                    .decode(&ids[i..end], false)
                    .map_err(|e| e.to_string())?,
            );
            i = end;
        }
        let refs: Vec<&str> = pieces.iter().map(String::as_str).collect();
        Ok(pack(&refs))
    }));
    match result {
        Ok(Ok(buffer)) => give(buffer, out_len),
        Ok(Err(e)) => {
            set_err(err, format!("sc_split_tokens: {e}"));
            ptr::null_mut()
        }
        Err(_) => {
            set_err(err, "sc_split_tokens: panicked");
            ptr::null_mut()
        }
    }
}

/// chonkie's `merge_splits`, from chonkie's own core.
///
/// `splits` is the packed layout this library returns, so Go can hand back a
/// buffer it built the same way. `counts` is one token count per split, in
/// order -- the core does not tokenize, so the caller supplies them (this is
/// what `sc_count` is for). Returns the merged splits, packed.
///
/// `out_counts`/`out_counts_len` receive one token count per merged split,
/// owned by the caller and freed with `sc_offsets_free`. They are not a
/// convenience. The core's merged count is the **sum** of the counts of the
/// splits it joined, and re-counting the merged text would not give that
/// number back: a SentencePiece vocabulary tokenizes `"a" + "b"` differently
/// from `"a"` and `"b"` apart, because the leading-space mark depends on what
/// came before. chonkie decides whether a merged split has to recurse to the
/// next level by comparing the core's sum against the chunk size
/// (`chonkie/chunker/recursive.py`, `_recursive_chunk`), so a Go caller that
/// recounted instead would take a different branch and cut in a different
/// place.
#[no_mangle]
pub unsafe extern "C" fn sc_merge_splits(
    splits: *const u8,
    splits_len: usize,
    counts: *const usize,
    counts_len: usize,
    size: usize,
    out_len: *mut usize,
    out_counts: *mut *mut usize,
    out_counts_len: *mut usize,
    err: *mut *mut c_char,
) -> *mut u8 {
    if size == 0 {
        set_err(err, "sc_merge_splits: size must be at least 1");
        return ptr::null_mut();
    }
    if out_counts.is_null() || out_counts_len.is_null() {
        set_err(err, "sc_merge_splits: the token-count out-parameters are null");
        return ptr::null_mut();
    }
    let unpacked = match unpack(splits, splits_len) {
        Ok(v) => v,
        Err(e) => {
            set_err(err, format!("sc_merge_splits: {e}"));
            return ptr::null_mut();
        }
    };
    if unpacked.len() != counts_len {
        set_err(
            err,
            format!(
                "sc_merge_splits: {} splits but {} counts",
                unpacked.len(),
                counts_len
            ),
        );
        return ptr::null_mut();
    }
    let counts: &[usize] = if counts_len == 0 {
        &[]
    } else if counts.is_null() {
        set_err(err, "sc_merge_splits: counts is null");
        return ptr::null_mut();
    } else {
        slice::from_raw_parts(counts, counts_len)
    };
    let result = catch_unwind(AssertUnwindSafe(|| {
        let merged = chunk::merge_splits(&unpacked, counts, size);
        let refs: Vec<&str> = merged.merged.iter().map(String::as_str).collect();
        (pack(&refs), merged.token_counts)
    }));
    match result {
        Ok((buffer, merged_counts)) => {
            *out_counts = give_offsets(merged_counts, out_counts_len);
            give(buffer, out_len)
        }
        Err(_) => {
            set_err(err, "sc_merge_splits: panicked");
            ptr::null_mut()
        }
    }
}

/// Reads the packed layout back into string slices borrowed from the buffer.
unsafe fn unpack<'a>(ptr: *const u8, len: usize) -> Result<Vec<&'a str>, String> {
    if ptr.is_null() {
        return if len == 0 {
            Ok(Vec::new())
        } else {
            Err("buffer is null".into())
        };
    }
    let bytes = slice::from_raw_parts(ptr, len);
    if bytes.len() < 4 {
        return Err("buffer is shorter than its own header".into());
    }
    let count = u32::from_le_bytes(bytes[0..4].try_into().unwrap()) as usize;
    let header = 4 + 4 * count;
    if bytes.len() < header {
        return Err(format!("buffer holds no lengths for {count} items"));
    }
    let mut items = Vec::with_capacity(count);
    let mut at = header;
    for i in 0..count {
        let start = 4 + 4 * i;
        let size = u32::from_le_bytes(bytes[start..start + 4].try_into().unwrap()) as usize;
        let end = at.checked_add(size).ok_or("item length overflows")?;
        if end > bytes.len() {
            return Err(format!("item {i} runs past the end of the buffer"));
        }
        items.push(std::str::from_utf8(&bytes[at..end]).map_err(|e| e.to_string())?);
        at = end;
    }
    Ok(items)
}

/// The version of chonkie's core this was built against, as a C string.
///
/// Go prints it beside the Python side's `chonkie-core` version so a drift
/// between the two is visible rather than inferred.
#[no_mangle]
pub extern "C" fn sc_core_version() -> *const c_char {
    // A literal with an explicit NUL: no allocation, nothing to free.
    concat!("0.9.2", "\0").as_ptr() as *const c_char
}

/// Hands an offset array to Go: pointer and count, ownership transferred.
unsafe fn give_offsets(offsets: Vec<usize>, out_count: *mut usize) -> *mut usize {
    let mut boxed = offsets.into_boxed_slice();
    let ptr = boxed.as_mut_ptr();
    let count = boxed.len();
    std::mem::forget(boxed);
    if !out_count.is_null() {
        *out_count = count;
    }
    ptr
}

/// Frees an offset array this library returned.
#[no_mangle]
pub unsafe extern "C" fn sc_offsets_free(ptr: *mut usize, count: usize) {
    if !ptr.is_null() && count > 0 {
        drop(Vec::from_raw_parts(ptr, count, count));
    }
}

/// Where to cut `text` so that each piece holds at most `size` tokens, as byte
/// offsets into `text` itself.
///
/// This is `sc_split_tokens` without the decode, and it exists because the
/// decode loses text. Decoding a slice of BGE-M3 ids does not give back the
/// bytes that produced them: SentencePiece has no newline token and carries a
/// leading space as a mark on the piece, so every newline comes back as a
/// space and a space on a piece boundary disappears. chonkie's own last level
/// does exactly that (`chonkie/chunker/recursive.py`, the `else` branch of
/// `_split_text`), which is why one chunk in the live corpus is not a
/// substring of the document it came from and so cannot be found in it again.
///
/// Every token the tokenizer produces carries the byte range of the input it
/// was built from, and those ranges are what this returns: cut at the start of
/// the token that begins each run, and the pieces are slices of the original.
/// Nothing is rendered, so nothing can be lost -- joining the pieces gives
/// `text` back exactly.
///
/// The array is the cut points, not the pieces: it starts at 0, ends at
/// `text.len()`, and so holds one more entry than there are pieces. An empty
/// `text` gives a single entry, 0, and so no pieces at all.
///
/// Returns null on failure, with the reason in `*err`. Free with
/// `sc_offsets_free`.
#[no_mangle]
pub unsafe extern "C" fn sc_split_offsets(
    handle: *mut c_void,
    text: *const u8,
    len: usize,
    size: usize,
    out_count: *mut usize,
    err: *mut *mut c_char,
) -> *mut usize {
    let handle = match (handle as *mut Handle).as_ref() {
        Some(h) => h,
        None => {
            set_err(err, "sc_split_offsets: tokenizer handle is null");
            return ptr::null_mut();
        }
    };
    let text = match as_str(text, len) {
        Some(t) => t,
        None => {
            set_err(err, "sc_split_offsets: text is not valid UTF-8");
            return ptr::null_mut();
        }
    };
    if size == 0 {
        set_err(err, "sc_split_offsets: size must be at least 1");
        return ptr::null_mut();
    }
    let result = catch_unwind(AssertUnwindSafe(|| -> Result<Vec<usize>, String> {
        let encoding = handle
            .tokenizer
            .encode(text, false)
            .map_err(|e| e.to_string())?;
        let spans = encoding.get_offsets();
        let mut cuts: Vec<usize> = Vec::with_capacity(spans.len() / size + 2);
        cuts.push(0);
        let mut i = size;
        while i < spans.len() {
            let mut at = spans[i].0;
            // Three things a token's span can do that a cut point must not.
            //
            // It can sit before the previous cut, or repeat it: a Metaspace
            // mark that consumed no input has an empty span, and a normalizer
            // that merged characters can map several tokens onto one byte. A
            // cut must move forward, so one that would not is dropped -- that
            // piece then carries up to twice `size` tokens, which the caller's
            // own recount will see. It is better than an empty piece or a
            // reversed slice.
            //
            // It can land inside a multi-byte character, if a normalizer
            // mapped part of one. Slicing there would split a rune, so the cut
            // moves to the next character boundary.
            //
            // And it can run past the end, for the same reason. Clamp it.
            let last = *cuts.last().expect("cuts always holds at least 0");
            if at > text.len() {
                at = text.len();
            }
            while at < text.len() && !text.is_char_boundary(at) {
                at += 1;
            }
            if at > last {
                cuts.push(at);
            }
            i += size;
        }
        if *cuts.last().expect("cuts always holds at least 0") != text.len() {
            cuts.push(text.len());
        }
        Ok(cuts)
    }));
    match result {
        Ok(Ok(cuts)) => give_offsets(cuts, out_count),
        Ok(Err(e)) => {
            set_err(err, format!("sc_split_offsets: {e}"));
            ptr::null_mut()
        }
        Err(_) => {
            set_err(err, "sc_split_offsets: panicked");
            ptr::null_mut()
        }
    }
}

/// Which side of a split the delimiter that caused it belongs to.
///
/// The numbers are the wire form Go passes; they are matched exhaustively and
/// an unknown one is an error rather than a default, so a caller that forgets
/// to say cannot silently get `Prev`.
fn include_delim_of(value: i32) -> Option<IncludeDelim> {
    match value {
        0 => Some(IncludeDelim::Prev),
        1 => Some(IncludeDelim::Next),
        2 => Some(IncludeDelim::None),
        _ => None,
    }
}

/// Hands offset pairs to Go flattened: `[start0, end0, start1, end1, ...]`.
///
/// Flattened rather than a struct array because the layout of a Rust tuple is
/// not guaranteed across the C boundary, and two `usize` in a row are.
/// `out_count` receives the number of `usize` written, so twice the number of
/// pairs. Free with `sc_offsets_free`.
unsafe fn give_spans(spans: Vec<(usize, usize)>, out_count: *mut usize) -> *mut usize {
    let mut flat: Vec<usize> = Vec::with_capacity(spans.len() * 2);
    for (start, end) in spans {
        flat.push(start);
        flat.push(end);
    }
    give_offsets(flat, out_count)
}

/// chonkie's delimiter levels: every place a multi-byte pattern occurs.
///
/// This is what `RecursiveChunker._split_text` calls for a level whose
/// delimiter list holds any delimiter longer than one character
/// (`chonkie_core.split_pattern_offsets`). The matching is leftmost-first
/// Aho-Corasick, so where two patterns start at the same byte the one listed
/// first wins -- which is why the order of a level's delimiters is part of
/// where it cuts, not a detail.
///
/// `patterns` is the packed string layout described at the top of this file.
/// `min_chars` is chonkie's `min_characters_per_chunk`: a cut is only taken
/// where the piece it closes and the piece that follows BOTH reach it, so the
/// short pieces around a one-word line are absorbed into their neighbours.
/// Despite the name the core measures it in **bytes**.
///
/// Returns null on failure, with the reason in `*err`.
#[no_mangle]
pub unsafe extern "C" fn sc_split_at_patterns(
    text: *const u8,
    len: usize,
    patterns: *const u8,
    patterns_len: usize,
    include_delim: i32,
    min_chars: usize,
    out_count: *mut usize,
    err: *mut *mut c_char,
) -> *mut usize {
    let text = match as_str(text, len) {
        Some(t) => t,
        None => {
            set_err(err, "sc_split_at_patterns: text is not valid UTF-8");
            return ptr::null_mut();
        }
    };
    let include = match include_delim_of(include_delim) {
        Some(i) => i,
        None => {
            set_err(
                err,
                format!("sc_split_at_patterns: include_delim {include_delim} is not 0, 1 or 2"),
            );
            return ptr::null_mut();
        }
    };
    let unpacked = match unpack(patterns, patterns_len) {
        Ok(v) => v,
        Err(e) => {
            set_err(err, format!("sc_split_at_patterns: patterns: {e}"));
            return ptr::null_mut();
        }
    };
    if unpacked.is_empty() {
        // chonkie never asks for a pattern level with no patterns, and the
        // core would answer "one segment, the whole text", which reads like a
        // result rather than the mistake it is. Refuse instead.
        set_err(err, "sc_split_at_patterns: no patterns given");
        return ptr::null_mut();
    }
    let patterns: Vec<&[u8]> = unpacked.iter().map(|p| p.as_bytes()).collect();
    let result = catch_unwind(AssertUnwindSafe(|| {
        chunk::split_at_patterns(text.as_bytes(), &patterns, include, min_chars)
    }));
    match result {
        Ok(spans) => give_spans(spans, out_count),
        Err(_) => {
            set_err(err, "sc_split_at_patterns: panicked");
            ptr::null_mut()
        }
    }
}

/// chonkie's whitespace level: every place one of `delimiters`' bytes occurs.
///
/// This is `chonkie_core.split_offsets`, the single-byte path
/// `RecursiveChunker._split_text` takes for `whitespace=True` (with a single
/// space as the delimiter set) and for any delimiter level whose delimiters
/// are all one character long.
///
/// `delimiters` is a byte SET, not a string to match: each of its bytes is a
/// delimiter on its own. That is chonkie's own doing -- it joins the level's
/// delimiters and encodes the result -- and it is why a level holding a
/// multi-byte delimiter must go to `sc_split_at_patterns` instead: passed
/// here, an em dash would become three unrelated byte delimiters, each of
/// which cuts inside a character.
///
/// Returns null on failure, with the reason in `*err`.
#[no_mangle]
pub unsafe extern "C" fn sc_split_at_delimiters(
    text: *const u8,
    len: usize,
    delimiters: *const u8,
    delimiters_len: usize,
    include_delim: i32,
    min_chars: usize,
    out_count: *mut usize,
    err: *mut *mut c_char,
) -> *mut usize {
    let text = match as_str(text, len) {
        Some(t) => t,
        None => {
            set_err(err, "sc_split_at_delimiters: text is not valid UTF-8");
            return ptr::null_mut();
        }
    };
    let include = match include_delim_of(include_delim) {
        Some(i) => i,
        None => {
            set_err(
                err,
                format!(
                    "sc_split_at_delimiters: include_delim {include_delim} is not 0, 1 or 2"
                ),
            );
            return ptr::null_mut();
        }
    };
    if delimiters.is_null() || delimiters_len == 0 {
        set_err(err, "sc_split_at_delimiters: no delimiters given");
        return ptr::null_mut();
    }
    let delimiters = slice::from_raw_parts(delimiters, delimiters_len);
    let result = catch_unwind(AssertUnwindSafe(|| {
        chunk::split_at_delimiters(text.as_bytes(), delimiters, include, min_chars)
    }));
    match result {
        Ok(spans) => give_spans(spans, out_count),
        Err(_) => {
            set_err(err, "sc_split_at_delimiters: panicked");
            ptr::null_mut()
        }
    }
}
