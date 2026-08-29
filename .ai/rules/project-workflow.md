# Project Workflow

## Before Editing

- Inspect the current working tree and the relevant source files before changing behavior.
- Read every task-specific rule selected by the index in `AGENTS.md`.
- Keep the change scoped to the user's request; do not fold unrelated cleanup into the patch.

## While Editing

- Update documentation in the same change whenever the API, environment variables, persisted data, installation requirements, or release behavior changes.
- Preserve package boundaries and avoid new dependencies unless the task genuinely requires them.
- Keep shell command arguments structured as separate `exec.Command` arguments; never introduce `sh -c` or `bash -c` for device management.
- Do not modify deployment state, publish releases, create tags, or push branches unless the user explicitly requests that side effect.

## Verification

After changing Go files:

1. Run `gofmt -w` on every changed `.go` file.
2. Run the narrowest relevant package tests.
3. Run `go test ./...` when testable behavior changed.
4. Run `go vet ./...`.
5. Run `go build -o awg-server .`.

Before handoff:

- Run `git diff --check`.
- Confirm no generated binary or `dist/` file appears in the diff.
- Review the final diff for accidental contract or documentation drift.
- Report the exact commands run and any verification that could not be completed locally.
- Distinguish deterministic fake/stub evidence from real Ubuntu systemd,
  package/module, kernel, reboot, handshake, and traffic qualification.
