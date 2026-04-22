//! Global document state and ObjId handle table.
//!
//! Manages the single document instance and a handle table that maps
//! u32 handles to Automerge ObjIds. Handle 0 is always ROOT.

use automerge::{AutoCommit, ObjId};
use std::cell::RefCell;

/// Handle table mapping u32 → ObjId
pub(crate) struct ObjHandleTable {
    entries: Vec<Option<ObjId>>,
}

impl ObjHandleTable {
    pub fn new() -> Self {
        Self {
            // Slot 0 is reserved for ROOT (represented as None = sentinel)
            entries: vec![None],
        }
    }

    /// Insert an ObjId and return its handle.
    /// If the ObjId is ROOT, returns 0.
    pub fn insert(&mut self, obj: ObjId) -> u32 {
        if obj == ObjId::Root {
            return 0;
        }
        // Find a free slot or append
        for (i, slot) in self.entries.iter_mut().enumerate().skip(1) {
            if slot.is_none() {
                *slot = Some(obj);
                return i as u32;
            }
        }
        let handle = self.entries.len() as u32;
        self.entries.push(Some(obj));
        handle
    }

    /// Resolve a handle to an ObjId. Handle 0 = ROOT.
    pub fn get(&self, handle: u32) -> Option<ObjId> {
        if handle == 0 {
            return Some(ObjId::Root);
        }
        let idx = handle as usize;
        if idx < self.entries.len() {
            self.entries[idx].clone()
        } else {
            None
        }
    }

    /// Free a handle slot. Cannot free ROOT (handle 0).
    pub fn remove(&mut self, handle: u32) {
        if handle == 0 {
            return;
        }
        let idx = handle as usize;
        if idx < self.entries.len() {
            self.entries[idx] = None;
        }
    }

    /// Clear all handles except ROOT.
    pub fn clear(&mut self) {
        for slot in self.entries.iter_mut().skip(1) {
            *slot = None;
        }
    }
}

// Global state (thread-local for WASI single-threaded execution)
thread_local! {
    pub(crate) static DOC: RefCell<Option<AutoCommit>> = RefCell::new(None);
    pub(crate) static OBJ_TABLE: RefCell<ObjHandleTable> = RefCell::new(ObjHandleTable::new());
    // Buffer for returning variable-length data to the caller
    pub(crate) static RETURN_BUF: RefCell<Vec<u8>> = RefCell::new(Vec::new());
}

/// Initialize the global document
pub(crate) fn init_doc(doc: AutoCommit) {
    DOC.with(|cell| {
        *cell.borrow_mut() = Some(doc);
    });
    OBJ_TABLE.with(|cell| {
        *cell.borrow_mut() = ObjHandleTable::new();
    });
    RETURN_BUF.with(|cell| {
        cell.borrow_mut().clear();
    });
}

/// Execute a closure with immutable doc reference
pub(crate) fn with_doc<F, R>(f: F) -> Option<R>
where
    F: FnOnce(&AutoCommit) -> R,
{
    DOC.with(|cell| {
        let doc_ref = cell.borrow();
        doc_ref.as_ref().map(f)
    })
}

/// Execute a closure with mutable doc reference
pub(crate) fn with_doc_mut<F, R>(f: F) -> Option<R>
where
    F: FnOnce(&mut AutoCommit) -> R,
{
    DOC.with(|cell| {
        let mut doc_ref = cell.borrow_mut();
        doc_ref.as_mut().map(f)
    })
}

/// Insert an ObjId into the handle table, return handle
pub(crate) fn register_obj(obj: ObjId) -> u32 {
    OBJ_TABLE.with(|cell| cell.borrow_mut().insert(obj))
}

/// Resolve a handle to an ObjId
pub(crate) fn resolve_obj(handle: u32) -> Option<ObjId> {
    OBJ_TABLE.with(|cell| cell.borrow().get(handle))
}

/// Free a handle
pub(crate) fn free_obj(handle: u32) {
    OBJ_TABLE.with(|cell| cell.borrow_mut().remove(handle));
}

/// Store data in the return buffer
pub(crate) fn set_return_buf(data: Vec<u8>) {
    RETURN_BUF.with(|cell| {
        *cell.borrow_mut() = data;
    });
}

/// Get the length of the return buffer
pub(crate) fn return_buf_len() -> usize {
    RETURN_BUF.with(|cell| cell.borrow().len())
}

/// Copy the return buffer to the given pointer
pub(crate) fn copy_return_buf(ptr: *mut u8) {
    RETURN_BUF.with(|cell| {
        let buf = cell.borrow();
        if !ptr.is_null() && !buf.is_empty() {
            unsafe {
                std::ptr::copy_nonoverlapping(buf.as_ptr(), ptr, buf.len());
            }
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_obj_handle_table_root() {
        let table = ObjHandleTable::new();
        assert_eq!(table.get(0), Some(ObjId::Root));
    }

    #[test]
    fn test_obj_handle_table_insert_get() {
        let mut table = ObjHandleTable::new();
        // We can't easily create non-root ObjIds in tests without a doc,
        // but we can verify ROOT behavior
        let h = table.insert(ObjId::Root);
        assert_eq!(h, 0); // ROOT always maps to 0
        assert_eq!(table.get(0), Some(ObjId::Root));
    }

    #[test]
    fn test_obj_handle_table_unknown_handle() {
        let table = ObjHandleTable::new();
        assert_eq!(table.get(999), None);
    }
}
