//! List CRDT operations with ObjId handle support and typed values.

use automerge::{ReadDoc, transaction::Transactable};
use crate::state::{with_doc, with_doc_mut, resolve_obj, register_obj, set_return_buf, return_buf_len, copy_return_buf};
use crate::value::{encode_value, parse_tagged_value, ParsedValue};

/// Put a typed value into a list.
///
/// If insert_flag is 1, inserts before the given index.
/// If insert_flag is 0, overwrites the value at the given index.
///
/// For object types, creates a child object and returns its handle as positive i32.
/// Returns 0 for scalar success, >0 handle for objects, negative for errors.
#[no_mangle]
pub extern "C" fn am_list_put(
    obj_handle: u32,
    index: usize,
    insert_flag: u8,
    tag: u8,
    val_ptr: *const u8,
    val_len: usize,
) -> i32 {
    let payload = if val_ptr.is_null() || val_len == 0 {
        &[] as &[u8]
    } else {
        unsafe { std::slice::from_raw_parts(val_ptr, val_len) }
    };

    let parsed = match parse_tagged_value(tag, payload) {
        Some(p) => p,
        None => return -1,
    };

    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -3,
    };

    let insert = insert_flag != 0;

    match parsed {
        ParsedValue::Scalar(scalar) => {
            let result = with_doc_mut(|doc| {
                if insert {
                    doc.insert(&obj_id, index, scalar)
                } else {
                    doc.put(&obj_id, index, scalar)
                }
            });
            match result {
                Some(Ok(_)) => 0,
                Some(Err(_)) => -5,
                None => -2,
            }
        }
        ParsedValue::Object(obj_type) => {
            let result = with_doc_mut(|doc| {
                if insert {
                    doc.insert_object(&obj_id, index, obj_type)
                } else {
                    doc.put_object(&obj_id, index, obj_type)
                }
            });
            match result {
                Some(Ok(new_obj_id)) => register_obj(new_obj_id) as i32,
                Some(Err(_)) => -5,
                None => -2,
            }
        }
    }
}

/// Get the byte length of a typed value at list[index].
///
/// Returns length, or 0 if out of bounds/error.
#[no_mangle]
pub extern "C" fn am_list_get_len(obj_handle: u32, index: usize) -> u32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    with_doc(|doc| {
        match doc.get(&obj_id, index) {
            Ok(Some((val, child_obj_id))) => {
                let child_handle = match &val {
                    automerge::Value::Object(_) => register_obj(child_obj_id),
                    _ => 0,
                };
                let encoded = encode_value(&val, child_handle);
                set_return_buf(encoded);
                return_buf_len() as u32
            }
            _ => {
                set_return_buf(vec![crate::value::TAG_VOID]);
                1
            }
        }
    }).unwrap_or(0)
}

/// Copy the typed value for the last am_list_get_len call into a buffer.
#[no_mangle]
pub extern "C" fn am_list_get(obj_handle: u32, index: usize, ptr_out: *mut u8) -> i32 {
    let _ = (obj_handle, index);
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Delete an element from a list at the given index.
///
/// Returns 0 on success.
#[no_mangle]
pub extern "C" fn am_list_delete(obj_handle: u32, index: usize) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -3,
    };

    match with_doc_mut(|doc| doc.delete(&obj_id, index)) {
        Some(Ok(_)) => 0,
        Some(Err(_)) => -5,
        None => -2,
    }
}

/// Get the number of elements in a list.
#[no_mangle]
pub extern "C" fn am_list_len(obj_handle: u32) -> u32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    with_doc(|doc| doc.length(&obj_id) as u32).unwrap_or(0)
}

/// Increment a counter stored at list[index].
#[no_mangle]
pub extern "C" fn am_list_increment(obj_handle: u32, index: usize, delta: i64) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -3,
    };

    match with_doc_mut(|doc| doc.increment(&obj_id, index, delta)) {
        Some(Ok(_)) => 0,
        Some(Err(_)) => -5,
        None => -2,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::value::{TAG_STRING, TAG_LIST};

    fn setup_list() -> u32 {
        crate::document::am_create();
        // Create a list at ROOT["items"]
        let key = b"items";
        let handle = crate::map_ops::am_map_put(0, key.as_ptr(), key.len(), TAG_LIST, std::ptr::null(), 0);
        assert!(handle > 0);
        handle as u32
    }

    #[test]
    fn test_list_insert_and_get() {
        let list_h = setup_list();
        let val = b"hello";
        assert_eq!(am_list_put(list_h, 0, 1, TAG_STRING, val.as_ptr(), val.len()), 0);

        assert_eq!(am_list_len(list_h), 1);

        let len = am_list_get_len(list_h, 0);
        assert!(len > 0);
        let mut buf = vec![0u8; len as usize];
        am_list_get(list_h, 0, buf.as_mut_ptr());
        assert_eq!(buf[0], TAG_STRING);
        assert_eq!(&buf[1..], b"hello");
    }

    #[test]
    fn test_list_delete() {
        let list_h = setup_list();
        let val = b"test";
        am_list_put(list_h, 0, 1, TAG_STRING, val.as_ptr(), val.len());
        assert_eq!(am_list_len(list_h), 1);

        assert_eq!(am_list_delete(list_h, 0), 0);
        assert_eq!(am_list_len(list_h), 0);
    }

    #[test]
    fn test_list_multiple_inserts() {
        let list_h = setup_list();
        let a = b"a";
        let b_val = b"b";
        let c = b"c";
        am_list_put(list_h, 0, 1, TAG_STRING, a.as_ptr(), a.len());
        am_list_put(list_h, 1, 1, TAG_STRING, b_val.as_ptr(), b_val.len());
        am_list_put(list_h, 2, 1, TAG_STRING, c.as_ptr(), c.len());

        assert_eq!(am_list_len(list_h), 3);

        // Verify order
        let len = am_list_get_len(list_h, 0);
        let mut buf = vec![0u8; len as usize];
        am_list_get(list_h, 0, buf.as_mut_ptr());
        assert_eq!(&buf[1..], b"a");
    }
}
