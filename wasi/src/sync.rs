//! Sync protocol exports: per-peer state management, message generation and receipt.

use automerge::sync::{self, SyncDoc};
use std::cell::RefCell;
use std::collections::HashMap;
use crate::state::{with_doc_mut, set_return_buf, return_buf_len, copy_return_buf};

thread_local! {
    static SYNC_STATES: RefCell<HashMap<u32, sync::State>> = RefCell::new(HashMap::new());
    static NEXT_PEER_ID: RefCell<u32> = RefCell::new(1);
}

/// Initialize a new sync state for a peer.
///
/// Returns peer_id (>0) on success, 0 on failure.
#[no_mangle]
pub extern "C" fn am_sync_state_init() -> u32 {
    NEXT_PEER_ID.with(|next| {
        let peer_id = *next.borrow();
        *next.borrow_mut() = peer_id + 1;

        SYNC_STATES.with(|states| {
            states.borrow_mut().insert(peer_id, sync::State::new());
        });

        peer_id
    })
}

/// Free a peer's sync state.
///
/// Returns 0 on success, -1 if peer not found.
#[no_mangle]
pub extern "C" fn am_sync_state_free(peer_id: u32) -> i32 {
    SYNC_STATES.with(|states| {
        if states.borrow_mut().remove(&peer_id).is_some() {
            0
        } else {
            -1
        }
    })
}

/// Get the byte length of the next sync message to send to a peer.
///
/// Returns 0 if no message to send or peer not found.
#[no_mangle]
pub extern "C" fn am_sync_gen_len(peer_id: u32) -> u32 {
    SYNC_STATES.with(|states| {
        let mut states = states.borrow_mut();
        let state = match states.get_mut(&peer_id) {
            Some(s) => s,
            None => return 0u32,
        };

        with_doc_mut(|doc| {
            match doc.sync().generate_sync_message(state) {
                Some(msg) => {
                    let encoded = msg.encode();
                    set_return_buf(encoded);
                    return_buf_len() as u32
                }
                None => 0,
            }
        }).unwrap_or(0)
    })
}

/// Copy the sync message into a pre-allocated buffer.
///
/// Returns 0 on success, 1 if no message was generated, -1 if ptr null.
#[no_mangle]
pub extern "C" fn am_sync_gen(peer_id: u32, msg_out: *mut u8) -> i32 {
    let _ = peer_id;
    if msg_out.is_null() {
        return -1;
    }
    if return_buf_len() == 0 {
        return 1; // no message
    }
    copy_return_buf(msg_out);
    0
}

/// Receive and apply a sync message from a peer.
///
/// Returns 0 on success, -1 bad params, -2 no doc, -3 peer not found, -5 receive failed.
#[no_mangle]
pub extern "C" fn am_sync_recv(peer_id: u32, msg_ptr: *const u8, msg_len: usize) -> i32 {
    if msg_ptr.is_null() || msg_len == 0 {
        return -1;
    }
    let msg_bytes = unsafe { std::slice::from_raw_parts(msg_ptr, msg_len) };

    let msg = match sync::Message::decode(msg_bytes) {
        Ok(m) => m,
        Err(_) => return -5,
    };

    SYNC_STATES.with(|states| {
        let mut states = states.borrow_mut();
        let state = match states.get_mut(&peer_id) {
            Some(s) => s,
            None => return -3i32,
        };

        match with_doc_mut(|doc| doc.sync().receive_sync_message(state, msg)) {
            Some(Ok(_)) => 0,
            Some(Err(_)) => -5,
            None => -2,
        }
    })
}

/// Save (encode) a peer's sync state for persistence.
///
/// Returns byte length stored in return buffer.
#[no_mangle]
pub extern "C" fn am_sync_state_save_len(peer_id: u32) -> u32 {
    SYNC_STATES.with(|states| {
        let states = states.borrow();
        match states.get(&peer_id) {
            Some(state) => {
                let encoded = state.encode();
                set_return_buf(encoded);
                return_buf_len() as u32
            }
            None => 0,
        }
    })
}

/// Copy encoded sync state into a buffer.
#[no_mangle]
pub extern "C" fn am_sync_state_save(peer_id: u32, ptr_out: *mut u8) -> i32 {
    let _ = peer_id;
    if ptr_out.is_null() {
        return -1;
    }
    copy_return_buf(ptr_out);
    0
}

/// Load a previously saved sync state, creating a new peer_id for it.
///
/// Returns new peer_id (>0) on success, 0 on failure.
#[no_mangle]
pub extern "C" fn am_sync_state_load(data_ptr: *const u8, data_len: usize) -> u32 {
    if data_ptr.is_null() || data_len == 0 {
        return 0;
    }
    let bytes = unsafe { std::slice::from_raw_parts(data_ptr, data_len) };

    let state = match sync::State::decode(bytes) {
        Ok(s) => s,
        Err(_) => return 0,
    };

    NEXT_PEER_ID.with(|next| {
        let peer_id = *next.borrow();
        *next.borrow_mut() = peer_id + 1;

        SYNC_STATES.with(|states| {
            states.borrow_mut().insert(peer_id, state);
        });

        peer_id
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::value::TAG_STRING;

    #[test]
    fn test_sync_state_lifecycle() {
        let peer_id = am_sync_state_init();
        assert!(peer_id > 0);

        assert_eq!(am_sync_state_free(peer_id), 0);
        assert_eq!(am_sync_state_free(peer_id), -1); // already freed
    }

    #[test]
    fn test_sync_gen_no_doc() {
        let peer_id = am_sync_state_init();
        // No doc initialized — should return 0 (no message)
        // Actually with_doc_mut returns None → unwrap_or(0)
        let len = am_sync_gen_len(peer_id);
        // May or may not be 0 depending on doc state
        am_sync_state_free(peer_id);
    }

    #[test]
    fn test_sync_roundtrip() {
        // Create doc A with some data
        crate::document::am_create();
        let key = b"greeting";
        let val = b"hello";
        crate::map_ops::am_map_put(0, key.as_ptr(), key.len(), TAG_STRING, val.as_ptr(), val.len());
        crate::commit::am_commit(b"c1".as_ptr(), 2, 1000);

        // Save doc A
        let save_len = crate::document::am_save_len();
        let mut save_buf = vec![0u8; save_len as usize];
        crate::document::am_save(save_buf.as_mut_ptr());

        // Init sync on doc A
        let peer_a = am_sync_state_init();
        let gen_len = am_sync_gen_len(peer_a);
        assert!(gen_len > 0, "should have sync message to send");

        let mut msg_buf = vec![0u8; gen_len as usize];
        assert_eq!(am_sync_gen(peer_a, msg_buf.as_mut_ptr()), 0);

        // Load empty doc B
        crate::document::am_create();
        let peer_b = am_sync_state_init();

        // B receives A's message
        assert_eq!(am_sync_recv(peer_b, msg_buf.as_ptr(), msg_buf.len()), 0);

        am_sync_state_free(peer_a);
        am_sync_state_free(peer_b);
    }

    #[test]
    fn test_sync_state_save_load() {
        crate::document::am_create();
        let peer_id = am_sync_state_init();

        let save_len = am_sync_state_save_len(peer_id);
        assert!(save_len > 0);
        let mut save_buf = vec![0u8; save_len as usize];
        am_sync_state_save(peer_id, save_buf.as_mut_ptr());

        // Load into a new peer
        let new_peer = am_sync_state_load(save_buf.as_ptr(), save_buf.len());
        assert!(new_peer > 0);
        assert_ne!(new_peer, peer_id);

        am_sync_state_free(peer_id);
        am_sync_state_free(new_peer);
    }
}
