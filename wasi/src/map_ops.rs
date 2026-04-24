//! Map CRDT operations with ObjId handle support and typed values.

use automerge::{ReadDoc, transaction::Transactable};
use crate::state::{with_doc, with_doc_mut, resolve_obj, register_obj, set_return_buf, return_buf_len, copy_return_buf};
use crate::value::{encode_value, parse_tagged_value, ParsedValue};

/// Put a typed value into a map at the given key.
///
/// Parameters:
///   obj_handle: handle to the map object (0 = ROOT)
///   key_ptr/key_len: the key string (UTF-8)
///   tag: value type tag (see value.rs)
///   val_ptr/val_len: value payload (interpretation depends on tag)
///
/// For object types (tag 0x09=Map, 0x0A=List, 0x0B=Text), creates a new
/// child object and returns its handle as a positive i32.
///
/// Returns: handle (>0) for objects, 0 for scalars on success,
///          -1 invalid params, -2 no doc, -3 bad obj handle,
///          -4 bad key, -5 put failed.
#[no_mangle]
pub extern "C" fn am_map_put(
    obj_handle: u32,
    key_ptr: *const u8,
    key_len: usize,
    tag: u8,
    val_ptr: *const u8,
    val_len: usize,
) -> i32 {
    if key_ptr.is_null() || key_len == 0 {
        return -1;
    }

    let key = match unsafe { std::str::from_utf8(std::slice::from_raw_parts(key_ptr, key_len)) } {
        Ok(s) => s.to_string(),
        Err(_) => return -4,
    };

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

    match parsed {
        ParsedValue::Scalar(scalar) => {
            match with_doc_mut(|doc| doc.put(&obj_id, &key, scalar)) {
                Some(Ok(_)) => 0,
                Some(Err(_)) => -5,
                None => -2,
            }
        }
        ParsedValue::Object(obj_type) => {
            match with_doc_mut(|doc| doc.put_object(&obj_id, &key, obj_type)) {
                Some(Ok(new_obj_id)) => {
                    register_obj(new_obj_id) as i32
                }
                Some(Err(_)) => -5,
                None => -2,
            }
        }
    }
}

/// Get the byte length of a typed value at map[key].
///
/// The value is stored in the return buffer for subsequent am_map_get_value call.
/// Returns length, or 0 if not found/error.
#[no_mangle]
pub extern "C" fn am_map_get_len(
    obj_handle: u32,
    key_ptr: *const u8,
    key_len: usize,
) -> u32 {
    if key_ptr.is_null() || key_len == 0 {
        return 0;
    }

    let key = match unsafe { std::str::from_utf8(std::slice::from_raw_parts(key_ptr, key_len)) } {
        Ok(s) => s,
        Err(_) => return 0,
    };

    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    with_doc(|doc| {
        match doc.get(&obj_id, key) {
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
                // Return void tag
                set_return_buf(vec![crate::value::TAG_VOID]);
                1
            }
        }
    }).unwrap_or(0)
}

/// Copy the typed value for the last am_map_get_len call into a buffer.
///
/// Returns 0 on success, -1 if ptr null.
#[no_mangle]
pub extern "C" fn am_map_get(
    obj_handle: u32,
    key_ptr: *const u8,
    key_len: usize,
    ptr_out: *mut u8,
) -> i32 {
    let _ = (obj_handle, key_ptr, key_len); // data already in return_buf
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Delete a key from a map.
///
/// Returns 0 on success, -1 bad params, -2 no doc, -3 bad handle, -5 delete failed.
#[no_mangle]
pub extern "C" fn am_map_delete(
    obj_handle: u32,
    key_ptr: *const u8,
    key_len: usize,
) -> i32 {
    if key_ptr.is_null() || key_len == 0 {
        return -1;
    }

    let key = match unsafe { std::str::from_utf8(std::slice::from_raw_parts(key_ptr, key_len)) } {
        Ok(s) => s,
        Err(_) => return -1,
    };

    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -3,
    };

    match with_doc_mut(|doc| doc.delete(&obj_id, key)) {
        Some(Ok(_)) => 0,
        Some(Err(_)) => -5,
        None => -2,
    }
}

/// Increment a counter stored at map[key].
///
/// Returns 0 on success.
#[no_mangle]
pub extern "C" fn am_map_increment(
    obj_handle: u32,
    key_ptr: *const u8,
    key_len: usize,
    delta: i64,
) -> i32 {
    if key_ptr.is_null() || key_len == 0 {
        return -1;
    }

    let key = match unsafe { std::str::from_utf8(std::slice::from_raw_parts(key_ptr, key_len)) } {
        Ok(s) => s,
        Err(_) => return -1,
    };

    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -3,
    };

    match with_doc_mut(|doc| doc.increment(&obj_id, key, delta)) {
        Some(Ok(_)) => 0,
        Some(Err(_)) => -5,
        None => -2,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::value::{TAG_STRING, TAG_INT64, TAG_MAP, TAG_VOID};

    fn setup() {
        crate::document::am_create();
    }

    #[test]
    fn test_map_put_get_string() {
        setup();
        let key = b"hello";
        let val = b"world";
        let result = am_map_put(0, key.as_ptr(), key.len(), TAG_STRING, val.as_ptr(), val.len());
        assert_eq!(result, 0);

        let len = am_map_get_len(0, key.as_ptr(), key.len());
        assert!(len > 0);
        let mut buf = vec![0u8; len as usize];
        assert_eq!(am_map_get(0, key.as_ptr(), key.len(), buf.as_mut_ptr()), 0);
        assert_eq!(buf[0], TAG_STRING);
        assert_eq!(&buf[1..], b"world");
    }

    #[test]
    fn test_map_put_get_int64() {
        setup();
        let key = b"num";
        let val = 42i64.to_le_bytes();
        let result = am_map_put(0, key.as_ptr(), key.len(), TAG_INT64, val.as_ptr(), val.len());
        assert_eq!(result, 0);

        let len = am_map_get_len(0, key.as_ptr(), key.len());
        assert_eq!(len, 9); // 1 tag + 8 bytes
        let mut buf = vec![0u8; len as usize];
        am_map_get(0, key.as_ptr(), key.len(), buf.as_mut_ptr());
        assert_eq!(buf[0], TAG_INT64);
        let v = i64::from_le_bytes(buf[1..9].try_into().unwrap());
        assert_eq!(v, 42);
    }

    #[test]
    fn test_map_put_object() {
        setup();
        let key = b"nested";
        let result = am_map_put(0, key.as_ptr(), key.len(), TAG_MAP, std::ptr::null(), 0);
        assert!(result > 0); // should return a handle > 0
    }

    #[test]
    fn test_map_delete() {
        setup();
        let key = b"todelete";
        let val = b"value";
        am_map_put(0, key.as_ptr(), key.len(), TAG_STRING, val.as_ptr(), val.len());
        assert_eq!(am_map_delete(0, key.as_ptr(), key.len()), 0);

        let len = am_map_get_len(0, key.as_ptr(), key.len());
        let mut buf = vec![0u8; len as usize];
        am_map_get(0, key.as_ptr(), key.len(), buf.as_mut_ptr());
        assert_eq!(buf[0], TAG_VOID);
    }

    #[test]
    fn test_map_get_nonexistent() {
        setup();
        let key = b"nonexistent";
        let len = am_map_get_len(0, key.as_ptr(), key.len());
        assert_eq!(len, 1); // just the VOID tag
        let mut buf = vec![0u8; 1];
        am_map_get(0, key.as_ptr(), key.len(), buf.as_mut_ptr());
        assert_eq!(buf[0], TAG_VOID);
    }
}
