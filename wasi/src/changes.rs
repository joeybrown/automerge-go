//! Change introspection and serialization exports.

use automerge::Change;
use crate::state::{with_doc_mut, set_return_buf, return_buf_len, copy_return_buf};

/// Get the number of current heads (DAG tips).
#[no_mangle]
pub extern "C" fn am_get_heads_count() -> u32 {
    with_doc_mut(|doc| doc.get_heads().len() as u32).unwrap_or(0)
}

/// Get all heads as concatenated 32-byte hashes.
///
/// First call am_get_heads_count() to know how many, then allocate
/// count * 32 bytes.
///
/// Returns total byte length stored in return buffer.
#[no_mangle]
pub extern "C" fn am_get_heads_len() -> u32 {
    with_doc_mut(|doc| {
        let heads = doc.get_heads();
        let mut buf = Vec::with_capacity(heads.len() * 32);
        for h in &heads {
            buf.extend_from_slice(&h.0);
        }
        set_return_buf(buf);
        return_buf_len() as u32
    }).unwrap_or(0)
}

/// Copy heads data into a buffer.
#[no_mangle]
pub extern "C" fn am_get_heads(ptr_out: *mut u8) -> i32 {
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Get the total byte count of serialized changes since given heads.
///
/// Each change is prefixed with its 4-byte LE length.
/// If heads_ptr is null (heads_count=0), returns ALL changes.
///
/// Returns total byte length stored in return buffer.
#[no_mangle]
pub extern "C" fn am_get_changes_len(heads_ptr: *const u8, heads_count: u32) -> u32 {
    let since_heads = parse_heads(heads_ptr, heads_count);

    with_doc_mut(|doc| {
        let changes = doc.get_changes(&since_heads);
        let mut buf = Vec::new();
        for change in changes {
            let raw = change.raw_bytes();
            buf.extend_from_slice(&(raw.len() as u32).to_le_bytes());
            buf.extend_from_slice(raw);
        }
        set_return_buf(buf);
        return_buf_len() as u32
    }).unwrap_or(0)
}

/// Copy the changes data into a buffer.
#[no_mangle]
pub extern "C" fn am_get_changes(heads_ptr: *const u8, heads_count: u32, ptr_out: *mut u8) -> i32 {
    let _ = (heads_ptr, heads_count); // data already in return_buf from _len call
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Apply serialized changes (as produced by am_get_changes) to the document.
///
/// Format: sequence of [4-byte LE length][raw change bytes].
///
/// Returns 0 on success, -1 bad params, -2 no doc, -5 apply failed.
#[no_mangle]
pub extern "C" fn am_apply_changes(changes_ptr: *const u8, changes_len: usize) -> i32 {
    if changes_ptr.is_null() || changes_len == 0 {
        return -1;
    }
    let data = unsafe { std::slice::from_raw_parts(changes_ptr, changes_len) };

    match with_doc_mut(|doc| {
        let mut offset = 0;
        while offset + 4 <= data.len() {
            let len = u32::from_le_bytes(data[offset..offset + 4].try_into().unwrap()) as usize;
            offset += 4;
            if offset + len > data.len() {
                return Err(());
            }
            let change_bytes = &data[offset..offset + len];
            offset += len;

            let change = match automerge::Change::from_bytes(change_bytes.to_vec()) {
                Ok(c) => c,
                Err(_) => return Err(()),
            };
            if let Err(_) = doc.apply_changes(vec![change]) {
                return Err(());
            }
        }
        Ok(())
    }) {
        Some(Ok(_)) => 0,
        Some(Err(_)) => -5,
        None => -2,
    }
}

/// Get a specific change by hash.
///
/// hash_ptr: pointer to 32-byte hash
///
/// Stores the raw change bytes (prefixed with 4-byte length) in return buffer.
/// Returns byte length, or 0 if not found.
#[no_mangle]
pub extern "C" fn am_get_change_by_hash_len(hash_ptr: *const u8) -> u32 {
    if hash_ptr.is_null() {
        return 0;
    }
    let hash_bytes = unsafe { std::slice::from_raw_parts(hash_ptr, 32) };
    let hash_arr: [u8; 32] = match hash_bytes.try_into() {
        Ok(a) => a,
        Err(_) => return 0,
    };
    let hash = automerge::ChangeHash(hash_arr);

    with_doc_mut(|doc| {
        match doc.get_change_by_hash(&hash) {
            Some(change) => {
                let raw = change.raw_bytes();
                let mut buf = Vec::with_capacity(raw.len());
                buf.extend_from_slice(raw);
                set_return_buf(buf);
                return_buf_len() as u32
            }
            None => 0,
        }
    }).unwrap_or(0)
}

/// Copy the change data into a buffer.
#[no_mangle]
pub extern "C" fn am_get_change_by_hash(hash_ptr: *const u8, ptr_out: *mut u8) -> i32 {
    let _ = hash_ptr;
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Extract all metadata from raw change bytes into a packed info buffer.
///
/// Format of output:
///   [32 bytes: hash]
///   [8 bytes: timestamp LE i64]
///   [8 bytes: seq LE u64]
///   [4 bytes: actor_hex_len LE u32]
///   [actor_hex_len bytes: actor hex string]
///   [4 bytes: message_len LE u32]
///   [message_len bytes: message UTF-8]
///   [4 bytes: deps_count LE u32]
///   [deps_count * 32 bytes: dependency hashes]
///
/// Returns total byte length stored in return buffer, or 0 on error.
#[no_mangle]
pub extern "C" fn am_change_info_len(raw_ptr: *const u8, raw_len: usize) -> u32 {
    if raw_ptr.is_null() || raw_len == 0 {
        return 0;
    }
    let raw = unsafe { std::slice::from_raw_parts(raw_ptr, raw_len) };
    let change = match Change::from_bytes(raw.to_vec()) {
        Ok(c) => c,
        Err(_) => return 0,
    };

    let hash = change.hash();
    let timestamp = change.timestamp();
    let seq = change.seq();
    let actor_hex = change.actor_id().to_hex_string();
    let message = change.message().map_or("", |v| v);
    let deps = change.deps();

    let mut buf = Vec::new();
    // hash (32 bytes)
    buf.extend_from_slice(&hash.0);
    // timestamp (8 bytes LE i64)
    buf.extend_from_slice(&timestamp.to_le_bytes());
    // seq (8 bytes LE u64)
    buf.extend_from_slice(&seq.to_le_bytes());
    // actor hex string (4-byte len + data)
    buf.extend_from_slice(&(actor_hex.len() as u32).to_le_bytes());
    buf.extend_from_slice(actor_hex.as_bytes());
    // message (4-byte len + data)
    buf.extend_from_slice(&(message.len() as u32).to_le_bytes());
    buf.extend_from_slice(message.as_bytes());
    // deps (4-byte count + 32 bytes each)
    buf.extend_from_slice(&(deps.len() as u32).to_le_bytes());
    for dep in deps {
        buf.extend_from_slice(&dep.0);
    }

    set_return_buf(buf);
    return_buf_len() as u32
}

/// Copy the change info into a pre-allocated buffer.
#[no_mangle]
pub extern "C" fn am_change_info(raw_ptr: *const u8, raw_len: usize, ptr_out: *mut u8) -> i32 {
    let _ = (raw_ptr, raw_len); // data already in return_buf from _len call
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Helper: parse heads from a pointer + count (each head is 32 bytes).
fn parse_heads(ptr: *const u8, count: u32) -> Vec<automerge::ChangeHash> {
    if ptr.is_null() || count == 0 {
        return Vec::new();
    }
    let bytes = unsafe { std::slice::from_raw_parts(ptr, (count as usize) * 32) };
    bytes
        .chunks_exact(32)
        .filter_map(|chunk| {
            let arr: [u8; 32] = chunk.try_into().ok()?;
            Some(automerge::ChangeHash(arr))
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::value::TAG_STRING;

    #[test]
    fn test_heads_empty_doc() {
        crate::document::am_create();
        assert_eq!(am_get_heads_count(), 0);
    }

    #[test]
    fn test_heads_after_commit() {
        crate::document::am_create();
        let key = b"k";
        let val = b"v";
        crate::map_ops::am_map_put(0, key.as_ptr(), key.len(), TAG_STRING, val.as_ptr(), val.len());
        crate::commit::am_commit(b"c1".as_ptr(), 2, 1000);

        assert_eq!(am_get_heads_count(), 1);
        let heads_byte_len = am_get_heads_len();
        assert_eq!(heads_byte_len, 32);

        let mut heads_buf = vec![0u8; 32];
        am_get_heads(heads_buf.as_mut_ptr());
        assert!(heads_buf.iter().any(|&b| b != 0));
    }

    #[test]
    fn test_get_changes_all() {
        crate::document::am_create();
        let key = b"k";
        let val = b"v";
        crate::map_ops::am_map_put(0, key.as_ptr(), key.len(), TAG_STRING, val.as_ptr(), val.len());
        crate::commit::am_commit(b"c1".as_ptr(), 2, 1000);

        let len = am_get_changes_len(std::ptr::null(), 0);
        assert!(len > 0);

        let mut buf = vec![0u8; len as usize];
        am_get_changes(std::ptr::null(), 0, buf.as_mut_ptr());

        // Should be parseable: 4-byte length + raw bytes
        let change_len = u32::from_le_bytes(buf[0..4].try_into().unwrap()) as usize;
        assert!(change_len > 0);
        assert_eq!(change_len + 4, buf.len());
    }

    #[test]
    fn test_get_change_by_hash() {
        crate::document::am_create();
        let key = b"k";
        let val = b"v";
        crate::map_ops::am_map_put(0, key.as_ptr(), key.len(), TAG_STRING, val.as_ptr(), val.len());
        crate::commit::am_commit(b"c1".as_ptr(), 2, 1000);

        // Get the head hash
        am_get_heads_len();
        let mut hash = vec![0u8; 32];
        am_get_heads(hash.as_mut_ptr());

        // Look up the change
        let change_len = am_get_change_by_hash_len(hash.as_ptr());
        assert!(change_len > 0);
    }
}
