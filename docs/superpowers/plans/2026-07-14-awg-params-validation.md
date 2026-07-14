# AWG Parameter Validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Validate all exposed AmneziaWG CPS parameters before state mutation or configuration rendering and return HTTP 400 for invalid API input.

**Architecture:** Add implementation-compatible validation to `internal/awg` in two stages: supplied override validation and merged effective-profile validation. HTTP handlers reject malformed overrides, the client manager enforces the full domain contract before create/update mutations, and `main` validates operator defaults before pool startup.

**Tech Stack:** Go 1.22+, standard library only, `net/http`, existing `AWGParams` and manager/pool architecture.

## Global Constraints

- Always preserve the package dependency direction `config <- awg <- {clients, usage} <- api <- main`.
- Keep the standard-library-only dependency policy.
- Preserve zero-as-inheritance semantics for integer API overrides.
- Preserve persisted-client restoration behavior; do not add a new strict restoration gate.
- Keep `Jmin = 50`, `Jmax = 1000`, and the current generated `S1`/`S2` values valid.
- Do not add Go test files.
- Update API, configuration, README, and shared-agent documentation in the same change.
- Do not edit or commit `awg-server` or files under `dist/`.

---

## File Map

- Create `internal/awg/validation.go`: validation constants, typed errors, header-range parsing, CPS parsing, override validation, and effective-profile validation.
- Modify `internal/api/handlers.go`: replace the four existing per-field validation blocks with unified override validation and map effective-profile errors to HTTP 400.
- Modify `internal/clients/manager.go`: validate raw and effective parameters before create/update mutations while leaving restoration unchanged.
- Modify `main.go`: validate the constructed default profile before pool creation.
- Modify `docs/api.md`: document exact API constraints and HTTP 400 behavior.
- Modify `docs/configuration.md`: document startup/default validation.
- Modify `README.md`: align examples with the accepted ranges and portable CPS tags.
- Modify `.ai/rules/architecture.md`: record the effective-profile validation boundary.
- Modify `.ai/rules/security.md`: record all CPS input-validation constraints.

---

### Task 1: Add AWG Domain Validation

**Files:**
- Create: `internal/awg/validation.go`

**Interfaces:**
- Consumes: `AWGParams`, `ValidatePort(int)`, `ValidateMTU(int)`, `ValidateDNS(string)`, and `ValidatePersistentKeepalive(*int)` from `internal/awg/params.go`.
- Produces: `ErrInvalidParams`, `ValidationError`, `ValidateOverrides(*AWGParams) error`, and `ValidateProfile(AWGParams) error`.

- [ ] **Step 1: Create the validation implementation**

Create `internal/awg/validation.go` with this implementation:

```go
package awg

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const MaxJunkPacketCount = 128
const MaxJunkPacketSize = 1280
const MaxS1 = 1132
const MaxS2 = 1188
const MaxS3 = 64
const MaxS4 = 32
const MaxCPSTagSize = 1000
const MaxCPSPacketSize = 1280

var ErrInvalidParams = errors.New("invalid awg params")

type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (e *ValidationError) Unwrap() error {
	return ErrInvalidParams
}

type headerRange struct {
	start uint32
	end   uint32
}

func ValidateOverrides(params *AWGParams) error {
	if params == nil {
		return nil
	}

	if err := ValidatePort(params.Port); err != nil {
		return validationFrom("port", err)
	}

	if err := ValidateMTU(params.MTU); err != nil {
		return validationFrom("mtu", err)
	}

	if err := ValidateDNS(params.DNS); err != nil {
		return validationFrom("dns", err)
	}

	if err := ValidatePersistentKeepalive(params.PersistentKeepalive); err != nil {
		return validationFrom("persistent_keepalive", err)
	}

	for _, value := range []struct {
		field string
		value int
		max   int
	}{
		{field: "jc", value: params.Jc, max: MaxJunkPacketCount},
		{field: "jmin", value: params.Jmin, max: MaxJunkPacketSize},
		{field: "jmax", value: params.Jmax, max: MaxJunkPacketSize},
		{field: "s1", value: params.S1, max: MaxS1},
		{field: "s2", value: params.S2, max: MaxS2},
		{field: "s3", value: params.S3, max: MaxS3},
		{field: "s4", value: params.S4, max: MaxS4},
	} {
		if value.value < 0 || value.value > value.max {
			return invalidParam(value.field, "must be between 0 and %d", value.max)
		}
	}

	for _, value := range []struct {
		field string
		value string
	}{
		{field: "h1", value: params.H1},
		{field: "h2", value: params.H2},
		{field: "h3", value: params.H3},
		{field: "h4", value: params.H4},
	} {
		if value.value == "" {
			continue
		}

		if _, err := parseHeaderRange(value.field, value.value); err != nil {
			return err
		}
	}

	for _, value := range []struct {
		field string
		value string
	}{
		{field: "i1", value: params.I1},
		{field: "i2", value: params.I2},
		{field: "i3", value: params.I3},
		{field: "i4", value: params.I4},
		{field: "i5", value: params.I5},
	} {
		if value.value == "" {
			continue
		}

		if err := validateCPS(value.field, value.value); err != nil {
			return err
		}
	}

	return nil
}

func ValidateProfile(params AWGParams) error {
	if err := ValidateOverrides(&params); err != nil {
		return err
	}

	if params.Jc > 0 {
		if params.Jmin <= 0 {
			return invalidParam("jmin", "must be greater than 0 when jc is enabled")
		}

		if params.Jmax <= 0 {
			return invalidParam("jmax", "must be greater than 0 when jc is enabled")
		}

		if params.Jmin >= params.Jmax {
			return invalidParam("jmin", "must be less than jmax")
		}
	}

	if params.S1+56 == params.S2 {
		return invalidParam("s2", "must not equal s1 + 56")
	}

	headers := []struct {
		field string
		value string
	}{
		{field: "h1", value: params.H1},
		{field: "h2", value: params.H2},
		{field: "h3", value: params.H3},
		{field: "h4", value: params.H4},
	}

	ranges := make([]headerRange, len(headers))

	for i, header := range headers {
		if header.value == "" {
			return invalidParam(header.field, "is required in the effective profile")
		}

		parsed, err := parseHeaderRange(header.field, header.value)
		if err != nil {
			return err
		}

		ranges[i] = parsed
	}

	for i := 0; i < len(ranges); i++ {
		for j := i + 1; j < len(ranges); j++ {
			if ranges[i].end < ranges[j].start || ranges[j].end < ranges[i].start {
				continue
			}

			return invalidParam(headers[j].field, "range must not overlap %s", headers[i].field)
		}
	}

	return nil
}

func parseHeaderRange(field, value string) (headerRange, error) {
	parts := strings.Split(value, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return headerRange{}, invalidParam(field, "must be an unsigned decimal value or start-end range")
	}

	start, err := parseUint32Decimal(parts[0])
	if err != nil {
		return headerRange{}, invalidParam(field, "must be an unsigned decimal value or start-end range")
	}

	end := start
	if len(parts) == 2 {
		end, err = parseUint32Decimal(parts[1])
		if err != nil {
			return headerRange{}, invalidParam(field, "must be an unsigned decimal value or start-end range")
		}
	}

	if start > end {
		return headerRange{}, invalidParam(field, "range start must not exceed range end")
	}

	return headerRange{start: start, end: end}, nil
}

func parseUint32Decimal(value string) (uint32, error) {
	if value == "" {
		return 0, errors.New("empty decimal value")
	}

	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, errors.New("non-decimal value")
		}
	}

	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, err
	}

	return uint32(parsed), nil
}

func validateCPS(field, value string) error {
	totalSize := 0

	for offset := 0; offset < len(value); {
		if value[offset] != '<' {
			return invalidParam(field, "must contain only supported CPS tags")
		}

		relativeEnd := strings.IndexByte(value[offset:], '>')
		if relativeEnd < 0 {
			return invalidParam(field, "contains an unterminated CPS tag")
		}

		end := offset + relativeEnd
		tag := value[offset+1 : end]

		tagSize, err := cpsTagSize(field, tag)
		if err != nil {
			return err
		}

		totalSize += tagSize
		if totalSize > MaxCPSPacketSize {
			return invalidParam(field, "expanded packet size must not exceed %d bytes", MaxCPSPacketSize)
		}

		offset = end + 1
	}

	return nil
}

func cpsTagSize(field, tag string) (int, error) {
	for i := 0; i < len(tag); i++ {
		if tag[i] < 0x20 || tag[i] == 0x7f {
			return 0, invalidParam(field, "contains a control character")
		}
	}

	if tag == "t" {
		return 4, nil
	}

	parts := strings.Split(tag, " ")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, invalidParam(field, "contains a malformed CPS tag")
	}

	switch parts[0] {
	case "b":
		if !strings.HasPrefix(parts[1], "0x") {
			return 0, invalidParam(field, "static byte tags must use the 0x prefix")
		}

		hexValue := parts[1][2:]
		if hexValue == "" || len(hexValue)%2 != 0 {
			return 0, invalidParam(field, "static byte tags require non-empty even-length hex")
		}

		for i := 0; i < len(hexValue); i++ {
			if !isHexDigit(hexValue[i]) {
				return 0, invalidParam(field, "static byte tags contain invalid hex")
			}
		}

		return len(hexValue) / 2, nil
	case "r", "rc", "rd":
		size, err := parseCPSSize(parts[1])
		if err != nil {
			return 0, invalidParam(field, "%s tag size must be between 0 and %d", parts[0], MaxCPSTagSize)
		}

		return size, nil
	default:
		return 0, invalidParam(field, "contains an unsupported CPS tag")
	}
}

func parseCPSSize(value string) (int, error) {
	if value == "" {
		return 0, errors.New("empty size")
	}

	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, errors.New("non-decimal size")
		}
	}

	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil || parsed > MaxCPSTagSize {
		return 0, errors.New("size out of range")
	}

	return int(parsed), nil
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func validationFrom(field string, err error) error {
	reason := strings.TrimPrefix(err.Error(), field+" ")
	return &ValidationError{Field: field, Reason: reason}
}

func invalidParam(field, format string, args ...any) error {
	return &ValidationError{Field: field, Reason: fmt.Sprintf(format, args...)}
}
```

- [ ] **Step 2: Format and compile the package**

Run:

```bash
gofmt -w internal/awg/validation.go
go test ./internal/awg
```

Expected: the package compiles and reports success, normally with `[no test files]`.

- [ ] **Step 3: Commit the domain validator**

```bash
git add internal/awg/validation.go
git commit -m "feat: validate AWG CPS parameters"
```

---

### Task 2: Enforce Validation Before Mutations

**Files:**
- Modify: `internal/api/handlers.go:72-92,98-113,134-154,160-177`
- Modify: `internal/clients/manager.go:80-108,135-161,310-398`
- Modify: `main.go:91-119`

**Interfaces:**
- Consumes: `awg.ValidateOverrides`, `awg.ValidateProfile`, and `awg.ErrInvalidParams` from Task 1.
- Produces: HTTP 400 classification and mutation-safe create/update/startup validation.

- [ ] **Step 1: Consolidate handler validation**

In both `handleCreateClient` and `handleUpdateClient`, replace the individual `ValidatePort`, `ValidateMTU`, `ValidateDNS`, and `ValidatePersistentKeepalive` blocks with:

```go
if err := awg.ValidateOverrides(req.AWGParams); err != nil {
	jsonError(w, err.Error(), http.StatusBadRequest)
	return
}
```

In both manager-error status switches, add this case before the other AWG error cases:

```go
case errors.Is(err, awg.ErrInvalidParams):
	status = http.StatusBadRequest
```

- [ ] **Step 2: Validate manager inputs and effective profiles**

Add this helper next to `effectiveParams` in `internal/clients/manager.go`:

```go
func (m *Manager) validatedParams(params *awg.AWGParams) (awg.AWGParams, error) {
	if err := awg.ValidateOverrides(params); err != nil {
		return awg.AWGParams{}, err
	}

	effective := m.effectiveParams(params)
	if err := awg.ValidateProfile(effective); err != nil {
		return awg.AWGParams{}, err
	}

	return effective, nil
}
```

In `CreateClient`, calculate the validated effective parameters immediately after the duplicate-client check and before key generation:

```go
effective, err := m.validatedParams(params)
if err != nil {
	return nil, err
}
```

Remove the later `effective := m.effectiveParams(params)` assignment and reuse the validated value.

In `UpdateClient`, replace:

```go
newParams := m.effectiveParams(params)
```

with:

```go
newParams, err := m.validatedParams(params)
if err != nil {
	return nil, err
}
```

Keep `NewManager` restoration on the existing direct `effectiveParams` path.

- [ ] **Step 3: Validate startup defaults**

Immediately after constructing `defaultParams` in `main.go` and before `awg.NewPool`, add:

```go
if err := awg.ValidateProfile(defaultParams); err != nil {
	log.Fatalf("validate default AWG params: %v", err)
}
```

- [ ] **Step 4: Format and compile all changed packages**

Run:

```bash
gofmt -w internal/api/handlers.go internal/clients/manager.go main.go
go test ./...
```

Expected: all packages compile successfully with no test failures.

- [ ] **Step 5: Commit the integration**

```bash
git add internal/api/handlers.go internal/clients/manager.go main.go
git commit -m "feat: reject invalid AWG profiles before mutation"
```

---

### Task 3: Document the Validation Contract

**Files:**
- Modify: `docs/api.md`
- Modify: `docs/configuration.md`
- Modify: `README.md`
- Modify: `.ai/rules/architecture.md`
- Modify: `.ai/rules/security.md`

**Interfaces:**
- Consumes: the exact constants and behavior implemented in Tasks 1 and 2.
- Produces: a user-facing and agent-facing contract that matches runtime behavior.

- [ ] **Step 1: Update the API contract**

In `docs/api.md`, replace the generic CPS field descriptions with these exact rules:

```markdown
- `jc`: 0-128; zero inherits the server default in API overrides.
- `jmin` and `jmax`: 0-1280; when effective `jc > 0`, both must be positive and `jmin < jmax`.
- `s1`: 0-1132, `s2`: 0-1188, `s3`: 0-64, `s4`: 0-32.
- `s2` must not equal `s1 + 56`.
- `h1`-`h4`: unsigned decimal `uint32` values or inclusive `start-end` ranges; effective ranges must not overlap.
- `i1`-`i5`: tag-only CPS strings supporting `b`, `t`, `r`, `rc`, and `rd`; dynamic tag sizes are 0-1000 and each expanded packet is at most 1280 bytes.
```

Add invalid `awg_params` to the documented HTTP 400 responses for create and update. Change all examples using `jmax: 2000` to `jmax: 1000`.

- [ ] **Step 2: Update configuration and README guidance**

In `docs/configuration.md`, state that the complete default profile is validated at startup and invalid CPS environment values prevent startup with a field-specific error.

In `README.md`:

- Change `"jmax":2000` to `"jmax":1000`.
- Change `AWG_I1='<b 0xc0><r 32><c><t>'` to `AWG_I1='<b 0xc0><r 32><t>'`.
- Add a concise validation table matching `docs/api.md`.

- [ ] **Step 3: Update shared agent rules**

In `.ai/rules/architecture.md`, add that API overrides are validated before merge, effective profiles are validated in create/update, defaults are validated at startup, and restoration remains grandfathered.

In `.ai/rules/security.md`, add the exact `J`, `S`, `H`, and `I` constraints from Step 1 and state that invalid values return HTTP 400 before mutation.

- [ ] **Step 4: Check documentation consistency and commit**

Run:

```bash
rg -n "jmax.*2000|<c>" README.md docs/api.md docs/configuration.md .ai/rules
git diff --check
```

Expected: no stale `jmax: 2000` or `<c>` example remains, and `git diff --check` exits successfully.

Commit:

```bash
git add README.md docs/api.md docs/configuration.md .ai/rules/architecture.md .ai/rules/security.md
git commit -m "docs: document AWG parameter validation"
```

---

### Task 4: Final Verification

**Files:**
- Inspect: all files changed since `origin/main`.

**Interfaces:**
- Consumes: completed code and documentation from Tasks 1-3.
- Produces: verified build and final handoff evidence.

- [ ] **Step 1: Run required repository checks**

```bash
go test ./...
go vet ./...
go build -o awg-server .
git diff --check origin/main...
```

Expected: every command exits with status 0.

- [ ] **Step 2: Review scope and artifacts**

```bash
git status --short
git diff --stat origin/main...
git diff origin/main... -- internal/awg/validation.go internal/api/handlers.go internal/clients/manager.go main.go README.md docs/api.md docs/configuration.md .ai/rules/architecture.md .ai/rules/security.md
```

Expected: only the planned source and Markdown files plus the design/plan documents are changed; `awg-server` and `dist/` do not appear in the diff.

- [ ] **Step 3: Report completion**

Report the implemented constraints, HTTP 400 behavior, preserved `Jmin = 50`, documentation changes, exact verification commands, and the absence of new test files.
