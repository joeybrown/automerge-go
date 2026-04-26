//! Rich text mark operations with ObjId handle support.
//!
//! Provides WASM exports for adding, removing, and querying formatting marks
//! on Text objects. Marks are CRDT-aware and merge correctly across peers.

use automerge::marks::{ExpandMark, Mark};
use automerge::transaction::Transactable;
use automerge::{ReadDoc, ScalarValue};

use crate::state::{resolve_obj, register_obj, set_last_error, set_return_buf, copy_return_buf, with_doc, with_doc_mut};

/// Add a mark (formatting) to a range of text.
///
/// # Parameters
/// - `obj_handle`: Handle to a Text object
/// - `name_ptr`: Pointer to mark name string (UTF-8)
/// - `name_len`: Length of name in bytes
/// - `value_ptr`: Pointer to mark value string (UTF-8)
/// - `value_len`: Length of value in bytes
/// - `start`: Start index of the range (inclusive)
/// - `end`: End index of the range (exclusive)
/// - `expand`: Expand mode (0=none, 1=before, 2=after, 3=both)
///
/// # Returns
/// - `0` on success
/// - `-1` on UTF-8 validation error
/// - `-2` on Automerge error
/// - `-3` if document not initialized
/// - `-4` if obj_handle is invalid
#[no_mangle]
pub extern "C" fn am_mark(
    obj_handle: u32,
    name_ptr: *const u8,
    name_len: usize,
    value_ptr: *const u8,
    value_len: usize,
    start: usize,
    end: usize,
    expand: u8,
) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -4,
    };

    if name_ptr.is_null() {
        return -1;
    }

    let name_slice = unsafe { std::slice::from_raw_parts(name_ptr, name_len) };
    let name = match std::str::from_utf8(name_slice) {
        Ok(s) => s,
        Err(_) => return -1,
    };

    let value = if value_ptr.is_null() || value_len == 0 {
        ""
    } else {
        let value_slice = unsafe { std::slice::from_raw_parts(value_ptr, value_len) };
        match std::str::from_utf8(value_slice) {
            Ok(s) => s,
            Err(_) => return -1,
        }
    };

    let expand_mode = match expand {
        0 => ExpandMark::None,
        1 => ExpandMark::Before,
        2 => ExpandMark::After,
        3 => ExpandMark::Both,
        _ => return -1,
    };

    let result = with_doc_mut(|doc| {
        let mark = Mark {
            start,
            end,
            name: name.into(),
            value: ScalarValue::Str(value.into()),
        };
        doc.mark(&obj_id, mark, expand_mode)
    });

    match result {
        Some(Ok(_)) => 0,
        Some(Err(e)) => {
            set_last_error(format!("{}", e));
            -2
        }
        None => -3,
    }
}

/// Remove a mark (formatting) from a range of text.
///
/// # Parameters
/// - `obj_handle`: Handle to a Text object
/// - `name_ptr`: Pointer to mark name string (UTF-8)
/// - `name_len`: Length of name in bytes
/// - `start`: Start index of the range (inclusive)
/// - `end`: End index of the range (exclusive)
/// - `expand`: Expand mode (0=none, 1=before, 2=after, 3=both)
///
/// # Returns
/// - `0` on success
/// - `-1` on UTF-8 validation error
/// - `-2` on Automerge error
/// - `-3` if document not initialized
/// - `-4` if obj_handle is invalid
#[no_mangle]
pub extern "C" fn am_unmark(
    obj_handle: u32,
    name_ptr: *const u8,
    name_len: usize,
    start: usize,
    end: usize,
    expand: u8,
) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -4,
    };

    if name_ptr.is_null() {
        return -1;
    }

    let name_slice = unsafe { std::slice::from_raw_parts(name_ptr, name_len) };
    let name = match std::str::from_utf8(name_slice) {
        Ok(s) => s,
        Err(_) => return -1,
    };

    let expand_mode = match expand {
        0 => ExpandMark::None,
        1 => ExpandMark::Before,
        2 => ExpandMark::After,
        3 => ExpandMark::Both,
        _ => return -1,
    };

    let result = with_doc_mut(|doc| {
        doc.unmark(&obj_id, name, start, end, expand_mode)
    });

    match result {
        Some(Ok(_)) => 0,
        Some(Err(e)) => {
            set_last_error(format!("{}", e));
            -2
        }
        None => -3,
    }
}

/// Get the byte length of the marks JSON string for a Text object.
///
/// The JSON format is: [{"name":"bold","value":"true","start":0,"end":5}, ...]
///
/// Call this first to allocate a buffer, then call `am_marks` to fill it.
///
/// # Returns
/// Byte length of the JSON string. Returns 0 on error or if no marks.
#[no_mangle]
pub extern "C" fn am_marks_len(obj_handle: u32) -> u32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    let result = with_doc(|doc| {
        match doc.marks(&obj_id) {
            Ok(marks) => {
                let json = marks_to_json(marks);
                let bytes = json.into_bytes();
                let len = bytes.len();
                set_return_buf(bytes);
                len as u32
            }
            Err(_) => 0,
        }
    });

    result.unwrap_or(0)
}

/// Copy the marks JSON into a pre-allocated buffer.
///
/// Must call `am_marks_len` first to populate the internal buffer.
///
/// # Returns
/// - `0` on success
/// - `-1` if ptr_out is null
#[no_mangle]
pub extern "C" fn am_marks(obj_handle: u32, ptr_out: *mut u8) -> i32 {
    let _ = obj_handle; // buffer was populated by am_marks_len
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Convert marks to JSON string.
fn marks_to_json(marks: Vec<Mark>) -> String {
    let mut json = String::from("[");
    let mut first = true;

    for mark in marks {
        if !first {
            json.push(',');
        }
        first = false;

        let value_str = match mark.value() {
            ScalarValue::Str(s) => s.to_string(),
            ScalarValue::Boolean(b) => b.to_string(),
            ScalarValue::Int(i) => i.to_string(),
            ScalarValue::Uint(u) => u.to_string(),
            ScalarValue::F64(f) => f.to_string(),
            _ => "null".to_string(),
        };

        // Escape the name and value for JSON safety
        let escaped_name = json_escape(mark.name());
        let escaped_value = json_escape(&value_str);

        json.push_str(&format!(
            r#"{{"name":"{}","value":"{}","start":{},"end":{}}}"#,
            escaped_name,
            escaped_value,
            mark.start,
            mark.end
        ));
    }

    json.push(']');
    json
}

/// Minimal JSON string escaping.
fn json_escape(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c if (c as u32) < 0x20 => {
                out.push_str(&format!("\\u{:04x}", c as u32));
            }
            c => out.push(c),
        }
    }
    out
}

// --- Spans ---

/// Get the byte length of the spans JSON string for a Text object.
///
/// Spans represent the rich-text structure of a Text object, including both
/// text runs (with their active marks) and block markers (embedded map objects).
///
/// The JSON format is:
/// ```json
/// [
///   {"type":"block","value":{"type":"paragraph","parents":[],"attrs":{}}},
///   {"type":"text","text":"hello","marks":{"bold":"true"}},
///   {"type":"text","text":" world","marks":{}}
/// ]
/// ```
///
/// Call this first to allocate a buffer, then call `am_spans` to fill it.
///
/// # Returns
/// Byte length of the JSON string. Returns 0 on error or if empty.
#[no_mangle]
pub extern "C" fn am_spans_len(obj_handle: u32) -> u32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return 0,
    };

    let result = with_doc(|doc| {
        match doc.spans(&obj_id) {
            Ok(spans) => {
                let json = spans_to_json(spans);
                let bytes = json.into_bytes();
                let len = bytes.len();
                set_return_buf(bytes);
                len as u32
            }
            Err(_) => 0,
        }
    });

    result.unwrap_or(0)
}

/// Copy the spans JSON into a pre-allocated buffer.
///
/// Must call `am_spans_len` first to populate the internal buffer.
///
/// # Returns
/// - `0` on success
/// - `-1` if ptr_out is null
#[no_mangle]
pub extern "C" fn am_spans(obj_handle: u32, ptr_out: *mut u8) -> i32 {
    let _ = obj_handle;
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Convert spans to JSON string.
fn spans_to_json(spans: automerge::iter::Spans<'_>) -> String {
    let mut json = String::from("[");
    let mut first = true;

    for span in spans {
        if !first {
            json.push(',');
        }
        first = false;

        match span {
            automerge::iter::Span::Text { text, marks } => {
                let escaped_text = json_escape(&text);
                json.push_str(&format!(r#"{{"type":"text","text":"{}","marks":{}}}"#,
                    escaped_text,
                    marks_option_to_json(&marks),
                ));
            }
            automerge::iter::Span::Block(map) => {
                json.push_str(&format!(r#"{{"type":"block","value":{}}}"#,
                    hydrate_map_to_json(&map),
                ));
            }
        }
    }

    json.push(']');
    json
}

/// Convert an Option<Arc<MarkSet>> to a JSON object string.
fn marks_option_to_json(marks: &Option<std::sync::Arc<automerge::marks::MarkSet>>) -> String {
    match marks {
        None => "{}".to_string(),
        Some(mark_set) => {
            let mut json = String::from("{");
            let mut first = true;
            for (name, value) in mark_set.iter() {
                if !first {
                    json.push(',');
                }
                first = false;
                json.push_str(&format!(r#""{}":"{}""#,
                    json_escape(name),
                    json_escape(&scalar_to_string(value)),
                ));
            }
            json.push('}');
            json
        }
    }
}

/// Convert a hydrate::Map to a JSON object string.
fn hydrate_map_to_json(map: &automerge::hydrate::Map) -> String {
    let mut json = String::from("{");
    let mut first = true;
    for (key, map_value) in map.iter() {
        if !first {
            json.push(',');
        }
        first = false;
        json.push_str(&format!(r#""{}":"#, json_escape(key)));
        json.push_str(&hydrate_value_to_json(&map_value.value));
    }
    json.push('}');
    json
}

/// Convert a hydrate::Value to a JSON string.
fn hydrate_value_to_json(val: &automerge::hydrate::Value) -> String {
    match val {
        automerge::hydrate::Value::Scalar(s) => scalar_to_json(s),
        automerge::hydrate::Value::Map(m) => hydrate_map_to_json(m),
        automerge::hydrate::Value::List(l) => {
            let mut json = String::from("[");
            let mut first = true;
            for list_val in l.iter() {
                if !first {
                    json.push(',');
                }
                first = false;
                json.push_str(&hydrate_value_to_json(&list_val.value));
            }
            json.push(']');
            json
        }
        automerge::hydrate::Value::Text(t) => {
            // Text nested inside a block — just return the string content
            format!(r#""{}""#, json_escape(&t.to_string()))
        }
    }
}

/// Convert a ScalarValue to its JSON representation.
fn scalar_to_json(s: &ScalarValue) -> String {
    match s {
        ScalarValue::Null => "null".to_string(),
        ScalarValue::Boolean(b) => b.to_string(),
        ScalarValue::Int(i) => i.to_string(),
        ScalarValue::Uint(u) => u.to_string(),
        ScalarValue::F64(f) => f.to_string(),
        ScalarValue::Str(s) => format!(r#""{}""#, json_escape(s)),
        ScalarValue::Bytes(b) => {
            // Base64-encode bytes for JSON
            use std::fmt::Write;
            let mut hex = String::with_capacity(b.len() * 2);
            for byte in b {
                write!(hex, "{:02x}", byte).unwrap();
            }
            format!(r#""{}""#, hex)
        }
        ScalarValue::Counter(c) => i64::from(c).to_string(),
        ScalarValue::Timestamp(t) => t.to_string(),
        ScalarValue::Unknown { .. } => "null".to_string(),
    }
}

/// Convert a ScalarValue to a string for mark values.
fn scalar_to_string(s: &ScalarValue) -> String {
    match s {
        ScalarValue::Str(s) => s.to_string(),
        ScalarValue::Boolean(b) => b.to_string(),
        ScalarValue::Int(i) => i.to_string(),
        ScalarValue::Uint(u) => u.to_string(),
        ScalarValue::F64(f) => f.to_string(),
        _ => "null".to_string(),
    }
}

// --- Block operations ---

/// Insert a block marker at the given index in a Text object.
///
/// A block marker is an embedded Map object that represents block-level
/// structure (paragraphs, headings, list items, etc.). It occupies one
/// character position in the text.
///
/// After calling this, use `am_map_put` on the returned handle to set
/// block properties like "type", "parents", "attrs".
///
/// # Parameters
/// - `obj_handle`: Handle to a Text object
/// - `index`: Position at which to insert the block marker
///
/// # Returns
/// - Positive value: the ObjHandle of the new block Map object
/// - `-2` on Automerge error
/// - `-3` if document not initialized
/// - `-4` if obj_handle is invalid
#[no_mangle]
pub extern "C" fn am_split_block(obj_handle: u32, index: usize) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -4,
    };

    let result = with_doc_mut(|doc| {
        doc.split_block(&obj_id, index)
    });

    match result {
        Some(Ok(block_id)) => register_obj(block_id.into()) as i32,
        Some(Err(e)) => {
            set_last_error(format!("{}", e));
            -2
        }
        None => -3,
    }
}

/// Delete the block marker at the given index in a Text object.
///
/// # Parameters
/// - `obj_handle`: Handle to a Text object
/// - `index`: Position of the block marker to delete
///
/// # Returns
/// - `0` on success
/// - `-2` on Automerge error
/// - `-3` if document not initialized
/// - `-4` if obj_handle is invalid
#[no_mangle]
pub extern "C" fn am_join_block(obj_handle: u32, index: usize) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -4,
    };

    let result = with_doc_mut(|doc| {
        doc.join_block(&obj_id, index)
    });

    match result {
        Some(Ok(_)) => 0,
        Some(Err(e)) => {
            set_last_error(format!("{}", e));
            -2
        }
        None => -3,
    }
}

/// Replace the block marker at the given index in a Text object.
///
/// The old block marker is removed and a new one is inserted at the same
/// position. Returns the handle to the new block Map.
///
/// # Parameters
/// - `obj_handle`: Handle to a Text object
/// - `index`: Position of the block marker to replace
///
/// # Returns
/// - Positive value: the ObjHandle of the new block Map object
/// - `-2` on Automerge error
/// - `-3` if document not initialized
/// - `-4` if obj_handle is invalid
#[no_mangle]
pub extern "C" fn am_replace_block(obj_handle: u32, index: usize) -> i32 {
    let obj_id = match resolve_obj(obj_handle) {
        Some(o) => o,
        None => return -4,
    };

    let result = with_doc_mut(|doc| {
        doc.replace_block(&obj_id, index)
    });

    match result {
        Some(Ok(block_id)) => register_obj(block_id.into()) as i32,
        Some(Err(e)) => {
            set_last_error(format!("{}", e));
            -2
        }
        None => -3,
    }
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
    fn test_mark_and_get_marks() {
        let text_h = setup_text();

        // Insert text
        let insert = b"Hello, World!";
        crate::text_ops::am_text_splice(text_h, 0, 0, insert.as_ptr(), insert.len());

        // Add a bold mark on "Hello"
        let name = b"bold";
        let value = b"true";
        let result = am_mark(
            text_h,
            name.as_ptr(), name.len(),
            value.as_ptr(), value.len(),
            0, 5, // start=0, end=5
            0,     // expand=none
        );
        assert_eq!(result, 0);

        // Get marks JSON length
        let json_len = am_marks_len(text_h);
        assert!(json_len > 2, "expected non-empty marks JSON, got len={}", json_len);

        // Read the marks JSON
        let mut buf = vec![0u8; json_len as usize];
        let result = am_marks(text_h, buf.as_mut_ptr());
        assert_eq!(result, 0);

        let json_str = std::str::from_utf8(&buf).unwrap();
        assert!(json_str.contains("bold"), "marks JSON should contain 'bold': {}", json_str);
        assert!(json_str.contains("true"), "marks JSON should contain 'true': {}", json_str);
    }

    #[test]
    fn test_unmark() {
        let text_h = setup_text();

        let insert = b"Hello, World!";
        crate::text_ops::am_text_splice(text_h, 0, 0, insert.as_ptr(), insert.len());

        // Add mark
        let name = b"bold";
        let value = b"true";
        am_mark(text_h, name.as_ptr(), name.len(), value.as_ptr(), value.len(), 0, 5, 0);

        // Remove mark
        let result = am_unmark(text_h, name.as_ptr(), name.len(), 0, 5, 0);
        assert_eq!(result, 0);

        // Verify no marks
        let json_len = am_marks_len(text_h);
        let mut buf = vec![0u8; json_len as usize];
        am_marks(text_h, buf.as_mut_ptr());
        let json_str = std::str::from_utf8(&buf).unwrap();
        assert_eq!(json_str, "[]");
    }

    #[test]
    fn test_mark_invalid_handle() {
        crate::document::am_create();
        let name = b"bold";
        let value = b"true";
        let result = am_mark(999, name.as_ptr(), name.len(), value.as_ptr(), value.len(), 0, 5, 0);
        assert_eq!(result, -4);
    }
}
