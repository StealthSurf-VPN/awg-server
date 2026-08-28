# Testing Rules

Use these rules when adding or changing Go tests. Prefer table-driven `*_test.go` files next to their source, in the same package, using `testing.T` and subtests via `t.Run`.

## When to Use

- The task adds tests for a named file, function, or behavior
- The target source file contains logic that can run without host networking or AmneziaWG kernel state
- Function under test is pure (no `exec.Command`, network, files, kernel calls)

**Do NOT use when:**
- Test file already exists — extend it instead of overwriting it
- The target only shells out and exposes no injectable command/filesystem
  dependency. Use a fake runner for deterministic unit tests of code such as
  `runtime.go`; reserve real kernel/module behavior for external qualification.
- A function uses real network sockets or unredirectable `/data/` paths

## Project Conventions

Follow `.ai/rules/code-style.md`:

| Convention | Example |
|---|---|
| Co-located, same package | `params.go` → `params_test.go`, `package awg` |
| Table-driven with subtests | `for _, tt := range tests { t.Run(tt.name, ...) }` |
| stdlib only | `import "testing"` — no testify, gomega, etc. |
| Vertical spacing between vars | blank lines between top-level decls |
| No comments unless non-obvious | trust the test name |

## Test Template

```go
package awg

import "testing"

func TestKey(t *testing.T) {
	tests := []struct {
		name   string
		params AWGParams
		want   string
	}{
		{
			name: "all fields populated",
			params: AWGParams{
				H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
				S1: 10, S2: 20, S3: 30, S4: 40,
			},
			want: "h1=1-2,h2=3-4,h3=5-6,h4=7-8,s1=10,s2=20,s3=30,s4=40",
		},
		{
			name:   "empty params",
			params: AWGParams{},
			want:   "h1=,h2=,h3=,h4=,s1=0,s2=0,s3=0,s4=0",
		},
		{
			name:   "Jc/Jmin/Jmax not in legacy helper key",
			params: AWGParams{Jc: 5, Jmin: 50, Jmax: 1000},
			want:   "h1=,h2=,h3=,h4=,s1=0,s2=0,s3=0,s4=0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.params.Key()
			if got != tt.want {
				t.Errorf("Key() = %q, want %q", got, tt.want)
			}
		})
	}
}
```

## Workflow

1. **Read source file** — list exported funcs/methods and identify pure ones
2. **For each pure function**, generate a `Test<Name>` with cases:
   - Happy path
   - Empty/zero/nil input
   - Boundary values (numeric ranges, string lengths)
   - Each error branch (if function returns `error`)
3. **For randomized funcs** (`GenerateParams`, `GenerateParamsV31`,
   `GeneratePrivateKey`), assert invariants over N≥100 iterations rather than
   exact values. For 3.1, verify fixed unique H values, S3 15-63, S4 12, and
   all S values at least 12.
4. **Place** as `<basename>_test.go` in the same directory
5. **Verify** — `go test ./<package>/...` passes; `go vet ./...` clean. For
   installer changes also run `bash scripts/install_test.sh`.

## Per-Package Hints

| File | Functions | Key invariants to assert |
|------|-----------|--------------------------|
| `internal/awg/keygen.go` | `GeneratePrivateKey`, `PublicKeyFromPrivate`, `Base64ToKey`, `KeyToBase64` | Roundtrip: `Base64ToKey(KeyToBase64(k)) == k`; private key clamping |
| `internal/awg/params.go` | `Key`, `ConfigLines`, `GenerateParams`, `GenerateParamsV31` | `Key` is a legacy helper, S3/S4 emitted, version-specific generated invariants and strict range/toggle rendering |
| `internal/awg/profile.go` | `NewLegacyProfile`, `NewAWG31Profile`, `Profile.Key` | Profile identity changes for version, server-applied fields, or header key only; client-only fields stay excluded; secrets do not serialize |
| `internal/awg/runtime.go` | `checkRuntime` with injected dependencies | Package minima, strict tools output, complete readback, collision safety, cleanup, and no secret in argv/error |
| `internal/clients/manager.go` | version-aware effective profile/update/restore helpers | Missing persisted version is legacy 2.0; omitted create uses default; migrations/snapshots/GC preserve rollback state |
| `internal/clients/storage.go` | storage decoding/cloning | Disk accepts canonical versions only; header key map deep-copy and invalid 3.1 state fail closed |
| `internal/awg/pool.go` | `resolvePort`, profile pool operations | Port range/shared-profile rules and `ProfileKey` separation |
| `internal/usage/collector.go` | parser for `awg show dump` output | Counter reset detection (current < previous → use current as delta) |

## Common Mistakes

- **External test package** (`package awg_test`) — this project uses internal tests for access to unexported helpers (`generateHRange`, `randIntRange`)
- **Asserting exact random output** — for `GenerateParams` and
  `GenerateParamsV31`, test invariants over many iterations (for example,
  `s1+56 != s2` always) instead.
- **Forgot `t.Parallel()`** is *not* a mistake here — project does not use it; matches existing style of leaving runs serial
- **Calling real `exec.Command` when an injected dependency exists** — unit
  tests must use the fake runner. A real Linux host with module and supported
  clients is still required for module reload, handshakes, and throughput.
- **Importing testify/gomega** — stdlib only; use `t.Errorf` / `t.Fatalf` directly
