//! Counter CRDT operations.
//!
//! Counters are special scalar values stored in maps or lists that support
//! increment operations (which merge correctly across peers, unlike put).

// Counter operations are handled by am_map_put (with TAG_COUNTER),
// am_map_increment, and am_list_increment.
// This module provides convenience wrappers if needed.

// The actual increment operations are already in map_ops.rs and list_ops.rs.
// This module exists for organizational completeness.
