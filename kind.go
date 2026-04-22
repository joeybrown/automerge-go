package automerge

import "fmt"

// Kind represents the underlying type of a Value
type Kind uint

var (
	// KindVoid indicates the value was not present
	KindVoid Kind = 0
	// KindBool indicates a bool
	KindBool Kind = 1
	// KindBytes indicates a []byte
	KindBytes Kind = 2
	// KindCounter indicates an *automerge.Counter
	KindCounter Kind = 3
	// KindFloat64 indicates a float64
	KindFloat64 Kind = 4
	// KindInt64 indicates an int
	KindInt64 Kind = 5
	// KindUint64 indicates a uint
	KindUint64 Kind = 6
	// KindNull indicates an explicit null was present
	KindNull Kind = 7
	// KindStr indicates a string
	KindStr Kind = 8
	// KindTime indicates a time.Time
	KindTime Kind = 9
	// KindUnknown indicates an unknown type from a future version of automerge
	KindUnknown Kind = 10

	// KindMap indicates an *automerge.Map
	KindMap Kind = 11
	// KindList indicates an *automerge.List
	KindList Kind = 12
	// KindText indicates an *automerge.Text
	KindText Kind = 13
)

var kindDescriptions = map[Kind]string{
	KindVoid:    "KindVoid",
	KindBool:    "KindBool",
	KindBytes:   "KindBytes",
	KindCounter: "KindCounter",
	KindFloat64: "KindFloat64",
	KindInt64:   "KindInt64",
	KindUint64:  "KindUint64",
	KindNull:    "KindNull",
	KindStr:     "KindStr",
	KindTime:    "KindTime",
	KindUnknown: "KindUnknown",
	KindMap:     "KindMap",
	KindList:    "KindList",
	KindText:    "KindText",
}

// String returns a human-readable representation of the Kind
func (k Kind) String() string {
	if s, ok := kindDescriptions[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%v)", uint(k))
}
