# Testing Rules

Use these rules when adding or changing Go tests. Prefer table-driven `*_test.go` files next to their source, in the same package, using `testing.T` and subtests via `t.Run`.

## When to Use

- The task adds tests for a named file, function, or behavior
- The target source file contains logic that can run without host networking or AmneziaWG kernel state
- Function under test is pure (no `exec.Command`, network, files, kernel calls)

**Do NOT use when:**
- Test file already exists — extend it instead of overwriting it
- Function shells out (`awg`, `ip`, `iptables`) — needs integration test on a Linux host with the kernel module, out of scope
- Function uses `os/exec`, real network sockets, or `/data/` paths

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
			name:   "Jc/Jmin/Jmax not in key (interface grouping)",
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
3. **For randomized funcs** (`GenerateParams`, `GeneratePrivateKey`), assert invariants over N≥100 iterations rather than exact values
4. **Place** as `<basename>_test.go` in the same directory
5. **Verify** — `go test ./<package>/...` passes; `go vet ./...` clean

## Per-Package Hints

| File | Functions | Key invariants to assert |
|------|-----------|--------------------------|
| `internal/awg/keygen.go` | `GeneratePrivateKey`, `PublicKeyFromPrivate`, `Base64ToKey`, `KeyToBase64` | Roundtrip: `Base64ToKey(KeyToBase64(k)) == k`; private key clamping |
| `internal/awg/params.go` | `Key`, `CLIArgs`, `ConfigLines`, `GenerateParams` | `Key` excludes Port/Jc/Jmin/Jmax/I1-I5; S3/S4 always emitted in `CLIArgs`/`ConfigLines`; `GenerateParams` honors `S1+56 != S2` |
| `internal/clients/manager.go` | `effectiveParams`, `allocateIP`, `ipToUint32`, `uint32ToIP` | `effectiveParams(nil)` returns defaults; per-field merge skips zero values; `allocateIP` skips server IP |
| `internal/awg/pool.go` | `resolvePort` | Port range 1024-65535; in-use rejected with `ErrPortInUse`; auto-assignment skips used ports |
| `internal/usage/collector.go` | parser for `awg show dump` output | Counter reset detection (current < previous → use current as delta) |

## Common Mistakes

- **External test package** (`package awg_test`) — this project uses internal tests for access to unexported helpers (`generateHRange`, `randIntRange`)
- **Asserting exact random output** — for `GenerateParams`, test invariants over many iterations (e.g. run 1000x and assert `s1+56 != s2` always)
- **Forgot `t.Parallel()`** is *not* a mistake here — project does not use it; matches existing style of leaving runs serial
- **Calling real `exec.Command`** — fails in any environment without root + kernel module; mark such tests with `t.Skip` or do not write them
- **Importing testify/gomega** — stdlib only; use `t.Errorf` / `t.Fatalf` directly
