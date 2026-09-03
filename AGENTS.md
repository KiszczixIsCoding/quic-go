# AGENTS.md — quic-go (fork)

## Repo structure

- Root `go.mod`: `github.com/quic-go/quic-go` — the modified quic-go library itself
- `_tul_project/`: Thesis project with its own `go.mod` (`module main`) and `replace github.com/quic-go/quic-go => ..` — always run from here
- `_tul_project/quic/`: Main thesis experiments (`two_connections.go`, `server_two_connections.go`)
- `_tul_project/streams/`: Alternative stream-based experiments
- `internal/wire/`: Frame types and parsing — custom frames live here
- `integrationtests/`: Separate from unit tests; NOT needed for library-only changes

## Custom fork modifications (not upstream)

- **Custom frame types** `TulCustomFrame` (0x21) and `SplitDataFrame` (0x22) in `internal/wire/frame_type.go`
- **Frame implementations**: `internal/wire/tul_custom_frame.go`, `internal/wire/split_data_frame.go`
- **Frame parser cases**: `internal/wire/frame_parser.go:173-179` — note `err = nil` workaround on line 179 for `SplitDataFrame`
- **Connection channels**: `connection.go:229-232` — added `sendSplitDataFrameChan`, `handleSplitDataFrameChan`, `handleTulCustomFrameChan`, `sendMyFrameChan`
- **Channel initialization**: `connection.go:562-564` — buffered channels (capacity 10)
- **Send path**: `connection.go:668-686` — main loop cases for custom frame channels, calls `framer.QueueControlFrame`
- **Receive path**: `connection.go:1952-1961` — `TulCustomFrame` uses non-blocking `select/default`, `SplitDataFrame` should also use non-blocking `select/default` (blocking send here causes deadlock when channel is full)
- **Public API**: `SendSplitDataFrame()`, `SendMyFrame()`, `GetSplitDataFrameChannel()`, `GetTulCustomFrameChannel()` on `*Conn`
- **qlog**: `qlog/frame.go` — custom frame type aliases and encoding stubs
- **Debug prints**: `fmt.Print`/`print` statements left in `connection.go`, `internal/wire/split_data_frame.go`, `internal/wire/frame_parser.go` — these are development artifacts, not production code

## Commands

```bash
# Lint (golangci-lint v2, required before commit)
golangci-lint run --timeout=3m

# Format check
gofmt -d .
gofumpt -l .

# Unit tests (excludes integrationtests/)
go test -v -shuffle on -cover ./...

# Single package test
go test -v ./internal/wire/...

# Mock generation (after changing exported interfaces)
go generate ./...

# Verify go.mod is clean
go mod tidy -diff

# Thesis project — run from _tul_project/
go run ./quic/two_connections.go    # client
go run ./quic/server_two_connections.go local1  # server (arg: azure|tul|local1|local2)
```

## Lint rules (.golangci.yml)

- No `math/rand` — use `math/rand/v2`
- No `crypto/rsa` — use `crypto/ed25519`
- No ginkgo/gomega — use standard Go tests
- `http3/` must NOT import `github.com/quic-go/quic-go/internal` (except `internal/synctest`)
- Formatters: `gofmt`, `gofumpt`, `goimports`

## Channel pattern for custom frames

When adding custom frame handling in `connection.go` receive path (`handleFrame`), always use non-blocking send:
```go
case *wire.SomeFrame:
    select {
    case c.handleSomeFrameChan <- frame:
    default:
    }
```
Blocking send (`c.chan <- frame`) will deadlock the main connection loop when the channel buffer is full.

## Testing notes

- `TIMESCALE_FACTOR` env var scales test timeouts (CI uses 10x for unit, 3x for integration)
- Some root-level tests require `-tags root` + sudo on Linux (`sys_conn_helper_linux_test.go`)
- Go 1.24 uses `GOEXPERIMENT=synctest`
