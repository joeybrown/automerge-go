//! Actor ID management exports.

use crate::state::{with_doc, with_doc_mut, set_return_buf, return_buf_len, copy_return_buf};

/// Get the byte length of the actor ID hex string.
#[no_mangle]
pub extern "C" fn am_get_actor_len() -> u32 {
    with_doc(|doc| {
        let actor = doc.get_actor();
        let hex = actor.to_hex_string();
        set_return_buf(hex.into_bytes());
        return_buf_len() as u32
    }).unwrap_or(0)
}

/// Copy the actor ID hex string into a pre-allocated buffer.
///
/// Returns 0 on success, -1 if ptr is null, -2 if doc not initialized.
#[no_mangle]
pub extern "C" fn am_get_actor(ptr_out: *mut u8) -> i32 {
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

/// Set the actor ID from a hex string.
///
/// Returns 0 on success, -1 if ptr is null, -2 if doc not initialized,
/// -3 if hex string is invalid.
#[no_mangle]
pub extern "C" fn am_set_actor(actor_ptr: *const u8, actor_len: usize) -> i32 {
    if actor_ptr.is_null() || actor_len == 0 {
        return -1;
    }
    let hex = unsafe { std::slice::from_raw_parts(actor_ptr, actor_len) };
    let hex_str = match std::str::from_utf8(hex) {
        Ok(s) => s,
        Err(_) => return -3,
    };

    // Parse hex string to bytes
    let bytes = match hex_to_bytes(hex_str) {
        Some(b) => b,
        None => return -3,
    };

    match with_doc_mut(|doc| {
        doc.set_actor(automerge::ActorId::from(bytes.as_slice()));
    }) {
        Some(_) => 0,
        None => -2,
    }
}

fn hex_to_bytes(hex: &str) -> Option<Vec<u8>> {
    if hex.len() % 2 != 0 {
        return None;
    }
    (0..hex.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&hex[i..i + 2], 16).ok())
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_hex_to_bytes() {
        assert_eq!(hex_to_bytes("deadbeef"), Some(vec![0xde, 0xad, 0xbe, 0xef]));
        assert_eq!(hex_to_bytes(""), Some(vec![]));
        assert_eq!(hex_to_bytes("0"), None); // odd length
        assert_eq!(hex_to_bytes("zz"), None); // invalid hex
    }

    #[test]
    fn test_actor_roundtrip() {
        crate::document::am_create();

        let len = am_get_actor_len();
        assert!(len > 0);
        let mut buf = vec![0u8; len as usize];
        assert_eq!(am_get_actor(buf.as_mut_ptr()), 0);

        let hex = std::str::from_utf8(&buf).unwrap();
        assert!(hex.len() > 0);
        assert!(hex.len() % 2 == 0);

        // Set a known actor
        let new_actor = b"deadbeefdeadbeefdeadbeefdeadbeef";
        assert_eq!(am_set_actor(new_actor.as_ptr(), new_actor.len()), 0);

        let len2 = am_get_actor_len();
        let mut buf2 = vec![0u8; len2 as usize];
        am_get_actor(buf2.as_mut_ptr());
        assert_eq!(&buf2, new_actor);
    }
}
