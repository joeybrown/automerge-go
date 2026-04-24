//! Cursor operations with ObjId handle support.
//!
//! Cursors track a stable position in a Text or List object that survives
//! concurrent edits. A cursor created at index 5 will automatically adjust
//! when other users insert or delete text before that position.

use automerge::ReadDoc;

use crate::state::{resolve_obj, set_return_buf, copy_return_buf, with_doc};

/// Get a cursor for a position in a Text or List object.
///
/// The cursor string is stored internally; call `am_get_cursor_str` to retrieve it.
///
/// # Parameters
/// - `obj_handle`: Handle to a Text or List object
/// - `index`: Character/element index to create cursor at
///
/// # Returns
/// - Positive value = byte length of cursor string
/// - `-1` if obj_handle is invalid
/// - `-2` if index is out of bounds or other error
/// - `-3` if document not initialized
#[no_mangle]
pub extern "C" fn am_get_cursor(obj_handle: u32, index: usize) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -1,
    };

    let result = with_doc(|doc| {
        match doc.get_cursor(&obj_id, index, None) {
            Ok(cursor) => {
                let cursor_str = cursor.to_string();
                let len = cursor_str.len();
                set_return_buf(cursor_str.into_bytes());
                len as i32
            }
            Err(_) => -2,
        }
    });

    result.unwrap_or(-3)
}

/// Copy the last cursor string into a pre-allocated buffer.
///
/// Must call `am_get_cursor` first to populate the internal buffer.
///
/// # Returns
/// - `0` on success
/// - `-1` if ptr_out is null
#[no_mangle]
pub extern "C" fn am_get_cursor_str(ptr_out: *mut u8) -> i32 {
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Look up the current index for a previously obtained cursor.
///
/// # Parameters
/// - `obj_handle`: Handle to a Text or List object
/// - `cursor_ptr`: Pointer to cursor string (UTF-8)
/// - `cursor_len`: Length of cursor string in bytes
///
/// # Returns
/// - Non-negative value = current index
/// - `-1` if obj_handle is invalid
/// - `-2` if cursor string is invalid
/// - `-3` if document not initialized
#[no_mangle]
pub extern "C" fn am_lookup_cursor(
    obj_handle: u32,
    cursor_ptr: *const u8,
    cursor_len: usize,
) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -1,
    };

    if cursor_ptr.is_null() {
        return -2;
    }

    let cursor_slice = unsafe { std::slice::from_raw_parts(cursor_ptr, cursor_len) };
    let cursor_str = match std::str::from_utf8(cursor_slice) {
        Ok(s) => s,
        Err(_) => return -2,
    };

    let result = with_doc(|doc| {
        use automerge::Cursor;
        let cursor = match Cursor::try_from(cursor_str) {
            Ok(c) => c,
            Err(_) => return -2,
        };
        match doc.get_cursor_position(&obj_id, &cursor, None) {
            Ok(index) => index as i32,
            Err(_) => -2,
        }
    });

    result.unwrap_or(-3)
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
    fn test_cursor_basic() {
        let text_h = setup_text();

        // Insert text
        let insert = b"Hello, World!";
        crate::text_ops::am_text_splice(text_h, 0, 0, insert.as_ptr(), insert.len());

        // Get cursor at index 7 (start of "World")
        let cursor_len = am_get_cursor(text_h, 7);
        assert!(cursor_len > 0, "expected positive cursor length, got {}", cursor_len);

        // Read cursor string
        let mut buf = vec![0u8; cursor_len as usize];
        let result = am_get_cursor_str(buf.as_mut_ptr());
        assert_eq!(result, 0);
        let cursor_str = std::str::from_utf8(&buf).unwrap();

        // Lookup cursor - should return index 7
        let index = am_lookup_cursor(text_h, cursor_str.as_ptr(), cursor_str.len());
        assert_eq!(index, 7);
    }

    #[test]
    fn test_cursor_survives_insert() {
        let text_h = setup_text();

        let insert = b"Hello, World!";
        crate::text_ops::am_text_splice(text_h, 0, 0, insert.as_ptr(), insert.len());

        // Cursor at index 7 ("W" in "World")
        let cursor_len = am_get_cursor(text_h, 7);
        assert!(cursor_len > 0);
        let mut buf = vec![0u8; cursor_len as usize];
        am_get_cursor_str(buf.as_mut_ptr());
        let cursor_str = std::str::from_utf8(&buf).unwrap().to_string();

        // Insert "Hey " at position 0
        let prefix = b"Hey ";
        crate::text_ops::am_text_splice(text_h, 0, 0, prefix.as_ptr(), prefix.len());

        // Cursor should now resolve to 11 (7 + 4)
        let new_index = am_lookup_cursor(text_h, cursor_str.as_ptr(), cursor_str.len());
        assert_eq!(new_index, 11);
    }

    #[test]
    fn test_cursor_invalid_handle() {
        crate::document::am_create();
        let result = am_get_cursor(999, 0);
        assert_eq!(result, -1);
    }
}
