# automerge-go

automerge-go provides the ability to interact with [automerge] documents.
It is a featureful wrapper around [automerge-rs] that uses [wazero] to run the
automerge engine as an embedded WASM module, requiring no C compiler or
platform-specific libraries — pure Go.

For package documentation, see the Go documentation at https://pkg.go.dev/github.com/automerge/automerge-go.

[automerge]: https://automerge.org
[automerge-rs]: https://github.com/automerge/automerge-rs
[wazero]: https://wazero.io

## Installation

```sh
go get github.com/automerge/automerge-go
```

No C compiler, shared libraries, or platform-specific build steps are required.
The automerge engine is compiled to WebAssembly and embedded in the Go binary
via `//go:embed`.

## Usage

```go
package main

import (
	"fmt"
	"github.com/automerge/automerge-go"
)

func main() {
	doc := automerge.New()
	doc.RootMap().Set("hello", "world")
	doc.Commit("first change")

	bytes := doc.Save()

	doc2, _ := automerge.Load(bytes)
	val, _ := automerge.As[string](doc2.RootMap().Get("hello"))
	fmt.Println(val) // "world"
}
```

## Architecture

Each `*Doc` gets its own isolated WASM module instance. The WASM module is
compiled once and cached, so creating additional documents is fast (~33μs).
All operations on a document are serialized via a mutex, making `*Doc`
safe for concurrent use from multiple goroutines.

## Building the WASM module from source

The pre-built `automerge.wasm` is embedded in the package. To rebuild it
from the Rust source:

```sh
make generate
```

Requires Rust with the `wasm32-wasip1` target:
```sh
rustup target add wasm32-wasip1
```