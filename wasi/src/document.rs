//! Document lifecycle management: create, save, load, merge, fork, clone.

use automerge::AutoCommit;
use crate::state::{init_doc, with_doc, with_doc_mut, set_return_buf, return_buf_len, copy_return_buf, set_last_error};

/// Create a new empty Automerge document.
///
/// Unlike the wazero-example's am_init, this does NOT create a hardcoded
/// ROOT["content"] text object. The document starts as an empty root map.
///
/// Returns 0 on success.
#[no_mangle]
pub extern "C" fn am_create() -> i32 {
    let doc = AutoCommit::new();
    init_doc(doc);
    0
}

/// Get the byte length of the serialized document.
#[no_mangle]
pub extern "C" fn am_save_len() -> u32 {
    with_doc_mut(|doc| {
        let bytes = doc.save();
        set_return_buf(bytes.to_vec());
        return_buf_len() as u32
    }).unwrap_or(0)
}

/// Save the document to a pre-allocated buffer.
///
/// Caller must first call am_save_len() to get size and allocate buffer.
/// Returns 0 on success, -1 if ptr is null, -2 if doc not initialized.
#[no_mangle]
pub extern "C" fn am_save(ptr_out: *mut u8) -> i32 {
    if ptr_out.is_null() {
        return -1;
    }
    match with_doc(|_| {
        copy_return_buf(ptr_out);
    }) {
        Some(_) => 0,
        None => -2,
    }
}

/// Load a document from serialized bytes, replacing any current document.
///
/// Returns 0 on success, -1 if ptr is null, -2 if deserialization fails.
#[no_mangle]
pub extern "C" fn am_load(ptr: *const u8, len: usize) -> i32 {
    if ptr.is_null() || len == 0 {
        return -1;
    }
    let bytes = unsafe { std::slice::from_raw_parts(ptr, len) };
    match AutoCommit::load(bytes) {
        Ok(doc) => {
            init_doc(doc);
            0
        }
        Err(_) => -2,
    }
}

/// Merge another (serialized) document into the current one.
///
/// The other document is passed as serialized bytes. This is the
/// CRDT merge — conflict-free and deterministic.
///
/// Returns 0 on success, -1 if ptr is null, -2 if doc not initialized,
/// -3 if other doc fails to load, -4 if merge fails.
#[no_mangle]
pub extern "C" fn am_merge(other_ptr: *const u8, other_len: usize) -> i32 {
    if other_ptr.is_null() || other_len == 0 {
        return -1;
    }
    let other_bytes = unsafe { std::slice::from_raw_parts(other_ptr, other_len) };

    let mut other_doc = match AutoCommit::load(other_bytes) {
        Ok(d) => d,
        Err(_) => return -3,
    };

    match with_doc_mut(|doc| doc.merge(&mut other_doc)) {
        Some(Ok(_)) => 0,
        Some(Err(e)) => {
            set_last_error(format!("{}", e));
            -4
        }
        None => -2,
    }
}

/// Fork the document, creating a new independent copy with a new actor ID.
///
/// If heads_ptr is null or heads_count is 0, forks at current state.
/// Otherwise forks at the given heads (each head is 32 bytes).
///
/// Returns 0 on success, -2 if doc not initialized, -3 if fork fails.
#[no_mangle]
pub extern "C" fn am_fork(heads_ptr: *const u8, heads_count: u32) -> i32 {
    match with_doc_mut(|doc| {
        let forked: Result<AutoCommit, _> = if heads_ptr.is_null() || heads_count == 0 {
            Ok(doc.fork())
        } else {
            let heads_bytes = unsafe {
                std::slice::from_raw_parts(heads_ptr, (heads_count as usize) * 32)
            };
            let heads: Vec<automerge::ChangeHash> = heads_bytes
                .chunks_exact(32)
                .filter_map(|chunk| {
                    let arr: [u8; 32] = chunk.try_into().ok()?;
                    Some(automerge::ChangeHash(arr))
                })
                .collect();
            doc.fork_at(&heads)
        };
        match forked {
            Ok(mut forked_doc) => {
                // Replace current doc with the fork
                // The caller should save the current doc first if needed
                // Actually: fork returns a NEW doc. We replace the global doc.
                // This is a design decision — Go side will manage multiple instances
                // by creating multiple WASM module instances.
                let saved = forked_doc.save();
                set_return_buf(saved.to_vec());
                0i32
            }
            Err(_) => -3,
        }
    }) {
        Some(code) => code,
        None => -2,
    }
}

/// Get the length of the fork result (saved bytes).
/// Call after am_fork to know how many bytes to allocate.
#[no_mangle]
pub extern "C" fn am_fork_len() -> u32 {
    return_buf_len() as u32
}

/// Copy the fork result into a buffer.
/// Call after am_fork + am_fork_len.
#[no_mangle]
pub extern "C" fn am_fork_get(ptr_out: *mut u8) -> i32 {
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn test_create_and_save_load() {
        assert_eq!(am_create(), 0);

        let save_len = am_save_len();
        assert!(save_len > 0);

        let mut buf = vec![0u8; save_len as usize];
        assert_eq!(am_save(buf.as_mut_ptr()), 0);

        // Load into a new doc
        assert_eq!(am_load(buf.as_ptr(), buf.len()), 0);

        // Verify it's a valid doc
        let save_len2 = am_save_len();
        assert!(save_len2 > 0);
    }

    #[test]
    fn test_load_invalid() {
        assert_eq!(am_load(b"garbage".as_ptr(), 7), -2);
    }

    #[test]
    fn test_load_null() {
        assert_eq!(am_load(std::ptr::null(), 0), -1);
    }

    #[test]
    fn test_merge() {
        // Create doc A
        am_create();
        let len_a = am_save_len();
        let mut buf_a = vec![0u8; len_a as usize];
        am_save(buf_a.as_mut_ptr());

        // Create doc B
        am_create();
        let len_b = am_save_len();
        let mut buf_b = vec![0u8; len_b as usize];
        am_save(buf_b.as_mut_ptr());

        // Load A and merge B into it
        am_load(buf_a.as_ptr(), buf_a.len());
        assert_eq!(am_merge(buf_b.as_ptr(), buf_b.len()), 0);
    }
}
