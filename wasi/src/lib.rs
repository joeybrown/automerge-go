//! Automerge WASI FFI Layer for automerge-go
//!
//! Provides a C-compatible FFI interface to the full Automerge API for use
//! with Go via wazero. Designed as a drop-in replacement for the automerge-c
//! static library used by the cgo bindings.
//!
//! ## Architecture
//!
//! ```text
//! Go (automerge-go) → wazero FFI → WASI exports (this crate) → Automerge Rust
//! ```

#![allow(clippy::not_unsafe_ptr_arg_deref)]
#![allow(clippy::missing_const_for_thread_local)]

mod memory;
mod state;
mod value;
mod document;
mod actor;
mod obj;
mod map_ops;
mod list_ops;
mod text_ops;
mod counter_ops;
mod commit;
mod changes;
mod sync;
