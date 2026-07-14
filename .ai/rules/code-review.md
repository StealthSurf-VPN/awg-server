# Go Code Review

Apply these checks when reviewing Go changes in this repository, including the final self-review after meaningful edits under `internal/` or `main.go`. Focus on concrete findings and keep the report terse.

## Procedure

1. **Load project context** (always):
   - Read `.ai/rules/code-style.md`, `.ai/rules/architecture.md`, `.ai/rules/api-patterns.md`, and `.ai/rules/security.md`
2. **Determine scope**:
   - If the caller named files, review those
   - Otherwise, inspect the changed Go surface relative to the task's base branch and review its immediate dependencies
3. **Run automated checks** and capture output:
   ```bash
   go vet ./...
   gofmt -l .
   rg -n 'fmt\.Errorf.*: %v' -g '*.go'
   rg -n 'sh -c|bash -c|exec\.Command\("sh"' -g '*.go'
   ```
4. **Manual review** of in-scope files against the categories below
5. **Report** with confidence filtering — only Critical and Major by default

## Check Categories

### Concurrency (highest priority)

Files most affected: `internal/awg/pool.go`, `internal/clients/manager.go`, `internal/usage/collector.go`.

- Every `Lock`/`RLock` paired via `defer mu.Unlock()`/`RUnlock()` — no early-return bypass
- Methods on `*Pool`, `*Manager` use **pointer receivers** — value receivers copy `sync.Mutex` and break locking (`go vet -copylocks` catches this; verify it's clean)
- All reads/writes to shared maps (`p.ifaces`, `p.usedPorts`, `m.clients`, `m.usedIPs`) happen under the relevant mutex
- No nested `Lock` of the same mutex (deadlock); no lock-ordering inversions if multiple are held
- `Pool.MigratePeer` is the only multi-step state transition under one lock — verify rollback paths in `pool.go` still hold the mutex

### exec.Command safety

Files: `internal/awg/pool.go` (iptables MASQUERADE), `internal/awg/device.go` (ip/awg).

- Args passed as separate strings; never `sh -c`, `bash -c`, or string concatenation into a single arg
- Trace user-controllable inputs to args:
  - Port: must be bounded `1024-65535` in `Pool.resolvePort` before reaching `exec.Command`
  - Interface name: `awgN` derived internally (safe)
  - Allowed IP, public key: validated via `net.ParseCIDR` / `awg.Base64ToKey` upstream
- Flag any `fmt.Sprintf` that builds an exec arg from API-provided strings (`AWGParams.H1-H4`, `I1-I5`)

### Error handling

Per `.ai/rules/code-style.md`:

- Wrapping uses `%w`, not `%v` — `fmt.Errorf("context: %w", err)`
- Sentinel errors compared with `errors.Is`, not `==`
- API 5xx responses go through `internal/api/handlers.go:writeError` — generic message to client, full error logged server-side
- No `panic` outside `init` or tests; no naked `os.Exit` outside `main`
- HTTP handlers return early on validation failure with appropriate status

### Goroutine and resource leaks

Files: `main.go`, `internal/usage/collector.go`, `internal/api/server.go`.

- Every `go func() { ... }()` has a shutdown path tied to context or signal
- `usage.Collector.Run(ctx)` exits on `ctx.Done()` — verify select includes it
- `defer cancel()` paired with every `context.WithCancel` / `WithTimeout` (see `main.go` shutdown path)
- HTTP response bodies closed (`defer resp.Body.Close()`); files closed (`defer f.Close()`)
- `pool.Close()` invoked on shutdown — destroys all interfaces and removes MASQUERADE rule

### Persistence and atomicity

- JSON writes go through `Storage.Save` (tmp + rename, perms 0600)
- No partial writes to `clients.json` or `usage.json` from anywhere except `Storage.Save` / `Collector.Save`
- Reads tolerate missing files (`os.IsNotExist` → empty struct)

### Project conventions

- Early returns over nested ifs
- Vertical spacing between top-level `var` decls and between statement groups
- No comments unless logic genuinely non-obvious — flag obvious-restating comments
- Standard library only beyond `golang.org/x/crypto`
- `net/http` ServeMux — no chi/gorilla/gin
- Multi-word filenames in `kebab-case.go`
- Russian for user communication, English in code

## Output Format

```
## Critical (block merge)
- <path>:<line> — <one-sentence issue>. Fix: <one-sentence>.

## Major (should fix)
- <path>:<line> — <one-sentence issue>. Fix: <one-sentence>.
```

Skip nitpicks. Skip generic "consider adding tests" unless the change introduces a clear test gap (e.g. new pure function in `internal/awg/`). If a category is clean, omit it. If everything is clean, say so in one line and stop.

Do NOT dump the whole codebase audit — review only the in-scope changes plus their immediate dependencies.
