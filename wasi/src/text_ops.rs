//! Text CRDT operations with ObjId handle support.

use automerge::ReadDoc;
use automerge::transaction::Transactable;
use crate::state::{with_doc, with_doc_mut, resolve_obj, set_return_buf, return_buf_len, copy_return_buf};

/// Splice text at the given position in a Text object.
///
/// Inserts `insert` text at `pos` after deleting `del_count` characters.
///
/// Returns 0 on success.
#[no_mangle]
pub extern "C" fn am_text_splice(
    obj_handle: u32,
    pos: usize,
    del_count: isize,
    insert_ptr: *const u8,
    insert_len: usize,
) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -3,
    };

    let insert_text = if insert_ptr.is_null() || insert_len == 0 {
        ""
    } else {
        match unsafe { std::str::from_utf8(std::slice::from_raw_parts(insert_ptr, insert_len)) } {
            Ok(s) => s,
            Err(_) => return -4,
        }
    };

    let del = if del_count < 0 { 0usize } else { del_count as usize };

    match with_doc_mut(|doc| doc.splice_text(&obj_id, pos, del as isize, insert_text)) {
        Some(Ok(_)) => 0,
        Some(Err(_)) => -5,
        None => -2,
    }
}

/// Get the byte length of the full text content of a Text object.
///
/// Stores the text in the return buffer. Returns byte count.
#[no_mangle]
pub extern "C" fn am_text_get_len(obj_handle: u32) -> u32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    with_doc(|doc| {
        match doc.text(&obj_id) {
            Ok(text) => {
                set_return_buf(text.into_bytes());
                return_buf_len() as u32
            }
            Err(_) => 0,
        }
    }).unwrap_or(0)
}

/// Copy the text content into a pre-allocated buffer.
#[no_mangle]
pub extern "C" fn am_text_get(obj_handle: u32, ptr_out: *mut u8) -> i32 {
    let _ = obj_handle;
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Get the length of the text in unicode codepoints (not bytes).
#[no_mangle]
pub extern "C" fn am_text_len_utf8(obj_handle: u32) -> u32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    with_doc(|doc| {
        doc.length(&obj_id) as u32
    }).unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::value::TAG_TEXT;

    fn setup_text() -> u32 {
        crate::document::am_create();
        let key = b"content";
        let handle = crate::map_ops::am_map_put(0, key.as_ptr(), key.len(), TAG_TEXT, std::ptr::null(), 0);
        assert!(handle > 0);
        handle as u32
    }

    #[test]
    fn test_text_splice_and_get() {
        let text_h = setup_text();

        let insert = b"Hello, World!";
        assert_eq!(am_text_splice(text_h, 0, 0, insert.as_ptr(), insert.len()), 0);

        let len = am_text_get_len(text_h);
        assert_eq!(len, 13);
        let mut buf = vec![0u8; len as usize];
        am_text_get(text_h, buf.as_mut_ptr());
        assert_eq!(&buf, b"Hello, World!");
    }

    #[test]
    fn test_text_splice_delete() {
        let text_h = setup_text();

        let insert = b"Hello, World!";
        am_text_splice(text_h, 0, 0, insert.as_ptr(), insert.len());

        // Delete "World!" and insert "Rust!"
        let replacement = b"Rust!";
        assert_eq!(am_text_splice(text_h, 7, 6, replacement.as_ptr(), replacement.len()), 0);

        let len = am_text_get_len(text_h);
        let mut buf = vec![0u8; len as usize];
        am_text_get(text_h, buf.as_mut_ptr());
        assert_eq!(&buf, b"Hello, Rust!");
    }

    #[test]
    fn test_text_unicode() {
        let text_h = setup_text();

        let insert = "Hello 🌍";
        let bytes = insert.as_bytes();
        assert_eq!(am_text_splice(text_h, 0, 0, bytes.as_ptr(), bytes.len()), 0);

        let byte_len = am_text_get_len(text_h);
        let mut buf = vec![0u8; byte_len as usize];
        am_text_get(text_h, buf.as_mut_ptr());
        assert_eq!(std::str::from_utf8(&buf).unwrap(), "Hello 🌍");

        // Codepoint length should be 7 (H, e, l, l, o, space, 🌍)
        let cp_len = am_text_len_utf8(text_h);
        assert_eq!(cp_len, 7);
    }
}
