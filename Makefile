.PHONY: test generate

test:
	go test -count=1 -race -timeout 120s ./...
	cd wasitest && go test -count=1 -race -timeout 120s ./...

generate:
	cd wasi && cargo build --target wasm32-wasip1 --release
	cp wasi/target/wasm32-wasip1/release/automerge_wasi.wasm internal/wazero/automerge.wasm
