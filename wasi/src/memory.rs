//! WASM memory management exports.
//!
//! Provides alloc/free for Go to manage WASM linear memory.

use std::alloc::{alloc, dealloc, Layout};

/// Allocate a buffer in WASM memory (8-byte aligned).
///
/// Returns a pointer to the allocated buffer, or null on failure.
#[no_mangle]
pub extern "C" fn am_alloc(size: usize) -> *mut u8 {
    if size == 0 {
        return std::ptr::null_mut();
    }
    let layout = match Layout::from_size_align(size, 8) {
        Ok(l) => l,
        Err(_) => return std::ptr::null_mut(),
    };
    unsafe { alloc(layout) }
}

/// Free a buffer previously allocated with am_alloc.
#[no_mangle]
pub extern "C" fn am_free(ptr: *mut u8, size: usize) {
    if ptr.is_null() || size == 0 {
        return;
    }
    let layout = match Layout::from_size_align(size, 8) {
        Ok(l) => l,
        Err(_) => return,
    };
    unsafe { dealloc(ptr, layout) }
}
