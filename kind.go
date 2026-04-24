package automerge

import "github.com/joeybrown/automerge-go/internal/backend"

// Kind represents the underlying type of a Value
type Kind = backend.Kind

var (
	// KindVoid indicates the value was not present
	KindVoid = backend.KindVoid
	// KindBool indicates a bool
	KindBool = backend.KindBool
	// KindBytes indicates a []byte
	KindBytes = backend.KindBytes
	// KindCounter indicates an *automerge.Counter
	KindCounter = backend.KindCounter
	// KindFloat64 indicates a float64
	KindFloat64 = backend.KindFloat64
	// KindInt64 indicates an int
	KindInt64 = backend.KindInt64
	// KindUint64 indicates a uint
	KindUint64 = backend.KindUint64
	// KindNull indicates an explicit null was present
	KindNull = backend.KindNull
	// KindStr indicates a string
	KindStr = backend.KindStr
	// KindTime indicates a time.Time
	KindTime = backend.KindTime
	// KindUnknown indicates an unknown type from a future version of automerge
	KindUnknown = backend.KindUnknown

	// KindMap indicates an *automerge.Map
	KindMap = backend.KindMap
	// KindList indicates an *automerge.List
	KindList = backend.KindList
	// KindText indicates an *automerge.Text
	KindText = backend.KindText
)
