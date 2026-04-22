//! Object introspection: type, size, keys, items, free handle.

use automerge::{ObjType, ReadDoc};
use crate::state::{with_doc, resolve_obj, free_obj as state_free_obj, register_obj, set_return_buf, return_buf_len, copy_return_buf};
use crate::value::encode_value;

/// Get the type of an object by handle.
///
/// Returns: 1 = Map, 2 = List, 3 = Text, -1 = invalid handle, -2 = no doc.
#[no_mangle]
pub extern "C" fn am_obj_type(obj_handle: u32) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -1,
    };

    match with_doc(|doc| {
        // ROOT is always a map
        if obj_handle == 0 {
            return 1i32;
        }
        match doc.object_type(&obj_id) {
            Ok(ObjType::Map) | Ok(ObjType::Table) => 1,
            Ok(ObjType::List) => 2,
            Ok(ObjType::Text) => 3,
            Err(_) => -1,
        }
    }) {
        Some(v) => v,
        None => -2,
    }
}

/// Get the number of entries in an object (keys for maps, elements for lists).
///
/// Returns count, or 0 if invalid/no doc.
#[no_mangle]
pub extern "C" fn am_obj_size(obj_handle: u32) -> u32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    with_doc(|doc| doc.length(&obj_id) as u32).unwrap_or(0)
}

/// Get the total byte length of null-separated keys for a map object.
///
/// Each key is followed by a null byte. Returns 0 if empty, invalid handle, or not a map.
#[no_mangle]
pub extern "C" fn am_obj_keys_len(obj_handle: u32) -> u32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    with_doc(|doc| {
        let keys: Vec<String> = doc.keys(&obj_id).collect();
        if keys.is_empty() {
            set_return_buf(Vec::new());
            return 0u32;
        }
        let mut buf = Vec::new();
        for key in &keys {
            buf.extend_from_slice(key.as_bytes());
            buf.push(0); // null separator
        }
        set_return_buf(buf);
        return_buf_len() as u32
    }).unwrap_or(0)
}

/// Copy null-separated keys into a pre-allocated buffer.
///
/// Returns 0 on success, -1 if ptr null.
#[no_mangle]
pub extern "C" fn am_obj_keys(obj_handle: u32, ptr_out: *mut u8) -> i32 {
    let _ = obj_handle; // keys were already prepared by am_obj_keys_len
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Get the total byte length of concatenated tagged values for all entries
/// in an object (map or list).
///
/// For maps, entries are: [4-byte key_len LE][key bytes][tagged value] per entry.
/// For lists, entries are: [tagged value] per entry, but each prefixed with 4-byte len.
///
/// Format per entry:
///   [4 bytes: entry_len LE] [4 bytes: key_len LE (0 for lists)] [key bytes] [tagged value]
///
/// Returns 0 if empty/invalid.
#[no_mangle]
pub extern "C" fn am_obj_items_len(obj_handle: u32) -> u32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    with_doc(|doc| {
        let mut buf = Vec::new();

        // Determine if this is a map or list
        let is_map = if obj_handle == 0 {
            true
        } else {
            matches!(doc.object_type(&obj_id), Ok(ObjType::Map) | Ok(ObjType::Table))
        };

        if is_map {
            for key in doc.keys(&obj_id) {
                if let Ok(Some((val, child_obj_id))) = doc.get(&obj_id, &key) {
                    let child_handle = match &val {
                        automerge::Value::Object(_) => register_obj(child_obj_id),
                        _ => 0,
                    };
                    let encoded = encode_value(&val, child_handle);
                    let key_bytes = key.as_bytes();

                    // Write: key_len(4) + key + value
                    let entry_len = 4 + key_bytes.len() + encoded.len();
                    buf.extend_from_slice(&(entry_len as u32).to_le_bytes());
                    buf.extend_from_slice(&(key_bytes.len() as u32).to_le_bytes());
                    buf.extend_from_slice(key_bytes);
                    buf.extend_from_slice(&encoded);
                }
            }
        } else {
            let len = doc.length(&obj_id);
            for i in 0..len {
                if let Ok(Some((val, child_obj_id))) = doc.get(&obj_id, i) {
                    let child_handle = match &val {
                        automerge::Value::Object(_) => register_obj(child_obj_id),
                        _ => 0,
                    };
                    let encoded = encode_value(&val, child_handle);

                    // Write: key_len=0 + value
                    let entry_len = 4 + encoded.len();
                    buf.extend_from_slice(&(entry_len as u32).to_le_bytes());
                    buf.extend_from_slice(&0u32.to_le_bytes()); // key_len = 0 for list
                    buf.extend_from_slice(&encoded);
                }
            }
        }

        set_return_buf(buf);
        return_buf_len() as u32
    }).unwrap_or(0)
}

/// Copy the items data into a pre-allocated buffer.
#[no_mangle]
pub extern "C" fn am_obj_items(obj_handle: u32, ptr_out: *mut u8) -> i32 {
    let _ = obj_handle;
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Free an object handle. Cannot free ROOT (handle 0).
///
/// Returns 0 on success, -1 if handle is 0 (ROOT).
#[no_mangle]
pub extern "C" fn am_obj_free(obj_handle: u32) -> i32 {
    if obj_handle == 0 {
        return -1;
    }
    state_free_obj(obj_handle);
    0
}
