//! Typed value encoding for passing values across the WASM boundary.
//!
//! Uses a tag-byte prefix protocol:
//!   0x00 = Null       (no payload)
//!   0x01 = Bool       (1 byte: 0 or 1)
//!   0x02 = Int64      (8 bytes LE)
//!   0x03 = Uint64     (8 bytes LE)
//!   0x04 = Float64    (8 bytes LE)
//!   0x05 = String     (remaining bytes UTF-8)
//!   0x06 = Bytes      (remaining bytes raw)
//!   0x07 = Counter    (8 bytes LE i64)
//!   0x08 = Timestamp  (8 bytes LE i64 millis)
//!   0x09 = Map        (4 bytes LE obj handle)
//!   0x0A = List       (4 bytes LE obj handle)
//!   0x0B = Text       (4 bytes LE obj handle)
//!   0xFF = Void       (no payload)

use automerge::{ObjType, ScalarValue, Value};

pub const TAG_NULL: u8 = 0x00;
pub const TAG_BOOL: u8 = 0x01;
pub const TAG_INT64: u8 = 0x02;
pub const TAG_UINT64: u8 = 0x03;
pub const TAG_FLOAT64: u8 = 0x04;
pub const TAG_STRING: u8 = 0x05;
pub const TAG_BYTES: u8 = 0x06;
pub const TAG_COUNTER: u8 = 0x07;
pub const TAG_TIMESTAMP: u8 = 0x08;
pub const TAG_MAP: u8 = 0x09;
pub const TAG_LIST: u8 = 0x0A;
pub const TAG_TEXT: u8 = 0x0B;
pub const TAG_VOID: u8 = 0xFF;

/// Encode an automerge Value into the tagged wire format.
/// For object types, `obj_handle` is the pre-registered handle.
pub fn encode_value(val: &Value<'_>, obj_handle: u32) -> Vec<u8> {
    match val {
        Value::Object(obj_type) => {
            let tag = match obj_type {
                ObjType::Map | ObjType::Table => TAG_MAP,
                ObjType::List => TAG_LIST,
                ObjType::Text => TAG_TEXT,
            };
            let mut buf = vec![tag];
            buf.extend_from_slice(&obj_handle.to_le_bytes());
            buf
        }
        Value::Scalar(s) => encode_scalar(s.as_ref()),
    }
}

/// Encode a scalar value
pub fn encode_scalar(s: &ScalarValue) -> Vec<u8> {
    match s {
        ScalarValue::Null => vec![TAG_NULL],
        ScalarValue::Boolean(b) => vec![TAG_BOOL, if *b { 1 } else { 0 }],
        ScalarValue::Int(i) => {
            let mut buf = vec![TAG_INT64];
            buf.extend_from_slice(&i.to_le_bytes());
            buf
        }
        ScalarValue::Uint(u) => {
            let mut buf = vec![TAG_UINT64];
            buf.extend_from_slice(&u.to_le_bytes());
            buf
        }
        ScalarValue::F64(f) => {
            let mut buf = vec![TAG_FLOAT64];
            buf.extend_from_slice(&f.to_le_bytes());
            buf
        }
        ScalarValue::Str(s) => {
            let mut buf = vec![TAG_STRING];
            buf.extend_from_slice(s.as_bytes());
            buf
        }
        ScalarValue::Bytes(b) => {
            let mut buf = vec![TAG_BYTES];
            buf.extend_from_slice(b);
            buf
        }
        ScalarValue::Counter(c) => {
            let mut buf = vec![TAG_COUNTER];
            buf.extend_from_slice(&i64::from(c).to_le_bytes());
            buf
        }
        ScalarValue::Timestamp(t) => {
            let mut buf = vec![TAG_TIMESTAMP];
            buf.extend_from_slice(&t.to_le_bytes());
            buf
        }
        ScalarValue::Unknown { .. } => vec![TAG_VOID],
    }
}

/// Parse a tag byte + payload into arguments for a map/list put operation.
/// Returns (tag, scalar_value) for scalar types.
/// For object types, returns the ObjType to create.
pub enum ParsedValue {
    Scalar(ScalarValue),
    Object(ObjType),
}

pub fn parse_tagged_value(tag: u8, payload: &[u8]) -> Option<ParsedValue> {
    match tag {
        TAG_NULL => Some(ParsedValue::Scalar(ScalarValue::Null)),
        TAG_BOOL => {
            if payload.is_empty() { return None; }
            Some(ParsedValue::Scalar(ScalarValue::Boolean(payload[0] != 0)))
        }
        TAG_INT64 => {
            if payload.len() < 8 { return None; }
            let v = i64::from_le_bytes(payload[..8].try_into().ok()?);
            Some(ParsedValue::Scalar(ScalarValue::Int(v)))
        }
        TAG_UINT64 => {
            if payload.len() < 8 { return None; }
            let v = u64::from_le_bytes(payload[..8].try_into().ok()?);
            Some(ParsedValue::Scalar(ScalarValue::Uint(v)))
        }
        TAG_FLOAT64 => {
            if payload.len() < 8 { return None; }
            let v = f64::from_le_bytes(payload[..8].try_into().ok()?);
            Some(ParsedValue::Scalar(ScalarValue::F64(v)))
        }
        TAG_STRING => {
            let s = std::str::from_utf8(payload).ok()?;
            Some(ParsedValue::Scalar(ScalarValue::Str(s.into())))
        }
        TAG_BYTES => {
            Some(ParsedValue::Scalar(ScalarValue::Bytes(payload.to_vec())))
        }
        TAG_COUNTER => {
            if payload.len() < 8 { return None; }
            let v = i64::from_le_bytes(payload[..8].try_into().ok()?);
            Some(ParsedValue::Scalar(ScalarValue::Counter(v.into())))
        }
        TAG_TIMESTAMP => {
            if payload.len() < 8 { return None; }
            let v = i64::from_le_bytes(payload[..8].try_into().ok()?);
            Some(ParsedValue::Scalar(ScalarValue::Timestamp(v)))
        }
        TAG_MAP => Some(ParsedValue::Object(ObjType::Map)),
        TAG_LIST => Some(ParsedValue::Object(ObjType::List)),
        TAG_TEXT => Some(ParsedValue::Object(ObjType::Text)),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_encode_null() {
        let encoded = encode_scalar(&ScalarValue::Null);
        assert_eq!(encoded, vec![TAG_NULL]);
    }

    #[test]
    fn test_encode_bool() {
        let encoded = encode_scalar(&ScalarValue::Boolean(true));
        assert_eq!(encoded, vec![TAG_BOOL, 1]);
    }

    #[test]
    fn test_encode_int64() {
        let encoded = encode_scalar(&ScalarValue::Int(42));
        assert_eq!(encoded.len(), 9);
        assert_eq!(encoded[0], TAG_INT64);
        let v = i64::from_le_bytes(encoded[1..9].try_into().unwrap());
        assert_eq!(v, 42);
    }

    #[test]
    fn test_encode_string() {
        let encoded = encode_scalar(&ScalarValue::Str("hello".into()));
        assert_eq!(encoded[0], TAG_STRING);
        assert_eq!(&encoded[1..], b"hello");
    }

    #[test]
    fn test_roundtrip_int64() {
        let original = ScalarValue::Int(-999);
        let encoded = encode_scalar(&original);
        if let Some(ParsedValue::Scalar(decoded)) = parse_tagged_value(encoded[0], &encoded[1..]) {
            assert_eq!(decoded, original);
        } else {
            panic!("roundtrip failed");
        }
    }

    #[test]
    fn test_roundtrip_string() {
        let original = ScalarValue::Str("hello world".into());
        let encoded = encode_scalar(&original);
        if let Some(ParsedValue::Scalar(decoded)) = parse_tagged_value(encoded[0], &encoded[1..]) {
            assert_eq!(decoded, original);
        } else {
            panic!("roundtrip failed");
        }
    }
}
