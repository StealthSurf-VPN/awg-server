# Client Listen Port Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional `awg_params.client_listen_port` API override that renders `ListenPort` in generated client configurations without changing server interface behavior.

**Architecture:** Store the optional integer in `internal/awg.AWGParams`, validate it with the other raw API overrides, merge it as a client-only effective parameter, and render it conditionally in `internal/clients`. Existing handler response and persistence paths automatically carry the field through `AWGParams`; server pool grouping and peer migration remain unchanged.

**Tech Stack:** Go 1.22+, standard library only, existing `net/http`, `AWGParams`, client manager, and JSON persistence architecture.

## Global Constraints

- `client_listen_port` is `0` or an integer in the inclusive range 1024-65535.
- Omitted or zero values omit `ListenPort` from the generated client configuration.
- The field must not affect `AWGParams.Key()`, `CLIArgs()`, server-side `port`, interface allocation, or peer migration.
- `PATCH` keeps replacing the complete `awg_params` object.
- Existing persisted clients remain compatible without migration.
- Add no dependencies and no Go test files.
- Keep code, comments, and project documentation in English.

---

### Task 1: Add the API field, validation, merge, and configuration rendering

**Files:**
- Modify: `internal/awg/params.go` (`AWGParams`)
- Modify: `internal/awg/validation.go` (`ValidateOverrides`)
- Modify: `internal/clients/manager.go` (`renderClientConfig`, `effectiveParams`)

**Interfaces:**
- Consumes: existing `AWGParams`, `ValidateOverrides(*AWGParams) error`, and `Manager.effectiveParams(*awg.AWGParams) awg.AWGParams` behavior.
- Produces: `AWGParams.ClientListenPort int` serialized as `client_listen_port`, validated before mutation and rendered as client `[Interface]` `ListenPort` when positive.

- [ ] **Step 1: Add the persisted API field**

Add the field next to the existing server-side `Port` field while keeping the two meanings explicit through their JSON names:

```go
type AWGParams struct {
	Port                int    `json:"port,omitempty"`
	ClientListenPort    int    `json:"client_listen_port,omitempty"`
	MTU                 int    `json:"mtu,omitempty"`
	DNS                 string `json:"dns,omitempty"`
	PersistentKeepalive *int   `json:"persistent_keepalive,omitempty"`
	Jc                  int    `json:"jc,omitempty"`
	Jmin                int    `json:"jmin,omitempty"`
	Jmax                int    `json:"jmax,omitempty"`
	S1                  int    `json:"s1,omitempty"`
	S2                  int    `json:"s2,omitempty"`
	S3                  int    `json:"s3,omitempty"`
	S4                  int    `json:"s4,omitempty"`
	H1                  string `json:"h1,omitempty"`
	H2                  string `json:"h2,omitempty"`
	H3                  string `json:"h3,omitempty"`
	H4                  string `json:"h4,omitempty"`
	I1                  string `json:"i1,omitempty"`
	I2                  string `json:"i2,omitempty"`
	I3                  string `json:"i3,omitempty"`
	I4                  string `json:"i4,omitempty"`
	I5                  string `json:"i5,omitempty"`
}
```

- [ ] **Step 2: Validate the raw override**

In `ValidateOverrides`, after validating `params.Port`, reject any nonzero client listen port outside the shared unprivileged-port range:

```go
if params.ClientListenPort != 0 && (params.ClientListenPort < MinPort || params.ClientListenPort > MaxPort) {
	return invalidParam("client_listen_port", "must be 0 or between %d and %d", MinPort, MaxPort)
}
```

This returns the existing safe `ValidationError`, which unwraps to `ErrInvalidParams`; the create and update handlers therefore return HTTP 400 without new handler branches.

- [ ] **Step 3: Merge the client-only override without changing grouping**

In `Manager.effectiveParams`, copy a positive override after the existing server-side port merge:

```go
if params.Port > 0 {
	result.Port = params.Port
}

if params.ClientListenPort > 0 {
	result.ClientListenPort = params.ClientListenPort
}
```

Do not add the field to `AWGParams.Key()`, `CLIArgs()`, `ConfigLines()`, or the `needsMigration` expression.

- [ ] **Step 4: Render `ListenPort` only when explicitly configured**

Build the beginning of the client configuration incrementally so the optional line remains inside `[Interface]`:

```go
cfg := fmt.Sprintf(`[Interface]
PrivateKey = %s`, client.PrivateKey)

if params.ClientListenPort > 0 {
	cfg += fmt.Sprintf(`
ListenPort = %d`, params.ClientListenPort)
}

cfg += fmt.Sprintf(`
Address = %s/32
DNS = %s
MTU = %d`, client.Address, params.DNS, params.MTU)
```

Leave the existing CPS and `[Peer]` rendering unchanged.

- [ ] **Step 5: Format and compile-check the changed packages**

Run:

```bash
gofmt -w internal/awg/params.go internal/awg/validation.go internal/clients/manager.go
go test ./...
```

Expected: formatting completes silently and all packages compile successfully, normally reporting `[no test files]` because no test files are being added.

- [ ] **Step 6: Commit the implementation**

```bash
git add internal/awg/params.go internal/awg/validation.go internal/clients/manager.go
git commit -m "feat: add client listen port override"
```

### Task 2: Document the public contract and shared project rules

**Files:**
- Modify: `docs/api.md`
- Modify: `docs/configuration.md`
- Modify: `README.md`
- Modify: `.ai/rules/architecture.md`
- Modify: `.ai/rules/api-patterns.md`
- Modify: `.ai/rules/security.md`

**Interfaces:**
- Consumes: `awg_params.client_listen_port` behavior implemented in Task 1.
- Produces: public API examples and durable agent instructions that distinguish the client listen port from server-side `awg_params.port`.

- [ ] **Step 1: Update API examples and the AWG params table**

Add `"client_listen_port": 54321` to representative create, update, and response examples. Add `ListenPort = 54321` after `PrivateKey` in the generated configuration example. Add this table row immediately after `port`:

```markdown
| `client_listen_port` | int | Local UDP listen port for the generated client `[Interface]`, inclusive range 1024-65535. If omitted or zero, `ListenPort` is omitted and the client selects a port automatically. Does not affect server interface grouping or `Endpoint`. |
```

State that changing this client-only value does not migrate the peer and takes effect after the regenerated configuration is downloaded and reapplied.

- [ ] **Step 2: Update persistence and operator documentation**

In `docs/configuration.md`, add `client_listen_port` to the per-client override prose and persisted JSON example. Explicitly state:

```markdown
The optional `client_listen_port` field accepts 1024-65535 and adds `ListenPort` to the generated client `[Interface]`; omission or zero leaves port selection automatic. It has no global environment default and does not affect the server-side interface port.
```

- [ ] **Step 3: Update README examples and behavior notes**

Add `client_listen_port: 54321` to the create/update curl examples and document the distinction from server-side `port`:

```markdown
Clients can set a local UDP port with `awg_params.client_listen_port` in the range 1024-65535. Omit it or set it to 0 to let the client choose automatically. This renders `ListenPort` in the client `[Interface]` and does not change the server `Endpoint` port.
```

- [ ] **Step 4: Update shared agent rules**

Update the architecture rule's client-only field lists, add a dedicated `ClientListenPort` entry, exclude it explicitly from grouping, and mention that the client manager renders it. Update the API pattern's PATCH description and add this security validation rule:

```markdown
- Per-client listen port accepts 0 for automatic client-side selection or values in the inclusive range 1024-65535; it does not reserve or expose a server-side port
```

- [ ] **Step 5: Verify the complete change**

Run:

```bash
go test ./...
go vet ./...
go build -o awg-server .
git diff --check
git status --short
```

Expected: tests/package compilation, vet, and build succeed; `git diff --check` is silent; status contains only the intended documentation/rule files and the ignored generated binary does not appear in the diff.

- [ ] **Step 6: Review and commit documentation**

Review `git diff origin/main...` and confirm the new field never reaches pool grouping or server `awg set` commands. Then run:

```bash
git add README.md docs/api.md docs/configuration.md .ai/rules/architecture.md .ai/rules/api-patterns.md .ai/rules/security.md
git commit -m "docs: document client listen port override"
```

- [ ] **Step 7: Report completion**

Report the API field, validation range, automatic behavior, generated configuration line, non-migration guarantee, documentation changes, exact verification commands, and the absence of new Go test files.
