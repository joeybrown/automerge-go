//! Commit, empty change, and incremental save/load exports.

use automerge::transaction::CommitOptions;
use crate::state::{with_doc_mut, set_return_buf, return_buf_len, copy_return_buf, set_last_error, clear_last_error};

/// Build CommitOptions from message + timestamp parameters.
fn build_commit_options(msg: Option<&str>, timestamp_millis: i64) -> CommitOptions {
    let mut opts = CommitOptions::default();
    if let Some(m) = msg {
        opts = opts.with_message(m);
    }
    if timestamp_millis != 0 {
        opts = opts.with_time(timestamp_millis);
    }
    opts
}

/// Commit pending operations with a message and timestamp.
///
/// Parameters:
///   msg_ptr/msg_len: commit message (UTF-8, can be null for empty message)
///   timestamp_millis: Unix timestamp in milliseconds (0 to omit)
///
/// On success, stores the change hash (32 bytes) in the return buffer.
/// Returns 0 on success, -2 if no doc, -3 if commit fails (e.g. no pending ops).
#[no_mangle]
pub extern "C" fn am_commit(
    msg_ptr: *const u8,
    msg_len: usize,
    timestamp_millis: i64,
) -> i32 {
    let msg: Option<String> = if msg_ptr.is_null() || msg_len == 0 {
        None
    } else {
        match unsafe { std::str::from_utf8(std::slice::from_raw_parts(msg_ptr, msg_len)) } {
            Ok(s) => Some(s.to_string()),
            Err(_) => {
                set_last_error("invalid UTF-8 in commit message".to_string());
                return -1;
            }
        }
    };

    clear_last_error();
    match with_doc_mut(|doc| {
        let opts = build_commit_options(msg.as_deref(), timestamp_millis);
        match doc.commit_with(opts) {
            Some(hash) => {
                set_return_buf(hash.0.to_vec());
                0i32
            }
            None => -3, // no pending ops
        }
    }) {
        Some(code) => code,
        None => -2,
    }
}

/// Get the length of the last commit hash (always 32 if commit succeeded).
#[no_mangle]
pub extern "C" fn am_commit_hash_len() -> u32 {
    return_buf_len() as u32
}

/// Copy the commit hash into a buffer.
#[no_mangle]
pub extern "C" fn am_commit_hash(ptr_out: *mut u8) -> i32 {
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Create an empty change (commit with no operations).
///
/// Returns 0 on success, -2 if no doc.
#[no_mangle]
pub extern "C" fn am_empty_change(
    msg_ptr: *const u8,
    msg_len: usize,
    timestamp_millis: i64,
) -> i32 {
    let msg: Option<String> = if msg_ptr.is_null() || msg_len == 0 {
        None
    } else {
        match unsafe { std::str::from_utf8(std::slice::from_raw_parts(msg_ptr, msg_len)) } {
            Ok(s) => Some(s.to_string()),
            Err(_) => return -1,
        }
    };

    match with_doc_mut(|doc| {
        let opts = build_commit_options(msg.as_deref(), timestamp_millis);
        let hash = doc.empty_change(opts);
        set_return_buf(hash.0.to_vec());
        0i32
    }) {
        Some(code) => code,
        None => -2,
    }
}

/// Save incremental changes since the last save.
///
/// Stores the result in the return buffer.
/// Returns byte count on success, 0 if no doc or no changes.
#[no_mangle]
pub extern "C" fn am_save_incremental_len() -> u32 {
    with_doc_mut(|doc| {
        let bytes = doc.save_incremental();
        set_return_buf(bytes);
        return_buf_len() as u32
    }).unwrap_or(0)
}

/// Copy incremental save bytes into a buffer.
#[no_mangle]
pub extern "C" fn am_save_incremental(ptr_out: *mut u8) -> i32 {
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Load incremental changes from bytes.
///
/// Returns 0 on success, -1 if ptr null, -2 if no doc, -5 if load fails.
#[no_mangle]
pub extern "C" fn am_load_incremental(ptr: *const u8, len: usize) -> i32 {
    if ptr.is_null() || len == 0 {
        return -1;
    }
    let bytes = unsafe { std::slice::from_raw_parts(ptr, len) };

    match with_doc_mut(|doc| doc.load_incremental(bytes)) {
        Some(Ok(_)) => 0,
        Some(Err(_)) => -5,
        None => -2,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::value::TAG_STRING;

    #[test]
    fn test_commit_and_hash() {
        crate::document::am_create();

        // Make a change
        let key = b"key";
        let val = b"val";
        crate::map_ops::am_map_put(0, key.as_ptr(), key.len(), TAG_STRING, val.as_ptr(), val.len());

        // Commit
        let msg = b"test commit";
        let result = am_commit(msg.as_ptr(), msg.len(), 1000);
        assert_eq!(result, 0);

        let hash_len = am_commit_hash_len();
        assert_eq!(hash_len, 32);

        let mut hash = vec![0u8; 32];
        am_commit_hash(hash.as_mut_ptr());
        // Hash should be non-zero
        assert!(hash.iter().any(|&b| b != 0));
    }

    #[test]
    fn test_commit_empty_fails() {
        crate::document::am_create();
        let msg = b"empty";
        let result = am_commit(msg.as_ptr(), msg.len(), 0);
        assert_eq!(result, -3); // no pending ops
    }

    #[test]
    fn test_incremental_save_load() {
        crate::document::am_create();

        // Make a change and commit
        let key = b"key";
        let val = b"val";
        crate::map_ops::am_map_put(0, key.as_ptr(), key.len(), TAG_STRING, val.as_ptr(), val.len());
        am_commit(b"c1".as_ptr(), 2, 1000);

        // Save full first (to establish baseline for incremental)
        let full_len = crate::document::am_save_len();
        let mut full_buf = vec![0u8; full_len as usize];
        crate::document::am_save(full_buf.as_mut_ptr());

        // Make another change
        let val2 = b"val2";
        crate::map_ops::am_map_put(0, key.as_ptr(), key.len(), TAG_STRING, val2.as_ptr(), val2.len());
        am_commit(b"c2".as_ptr(), 2, 2000);

        // Save incremental
        let inc_len = am_save_incremental_len();
        assert!(inc_len > 0);
        let mut inc_buf = vec![0u8; inc_len as usize];
        am_save_incremental(inc_buf.as_mut_ptr());

        // Load the full save into a fresh doc, then apply incremental
        crate::document::am_load(full_buf.as_ptr(), full_buf.len());
        assert_eq!(am_load_incremental(inc_buf.as_ptr(), inc_buf.len()), 0);
    }
}
