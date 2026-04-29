---
name: release
description: Use when cutting a new awg-server release — bumping version, updating CHANGELOG.md, creating git tag, and publishing to GitHub Releases with cross-platform binaries. User-invoked only (push and release side effects).
disable-model-invocation: true
---

# release

Cuts a new awg-server version: bumps version, updates `CHANGELOG.md`, builds cross-platform binaries via `make build-all`, tags, pushes, and creates a GitHub release. Deployed servers pick up the new binary via `awg-server update`.

## When to Use

- User says "release", "/release", "cut a release", or specifies a version
- All target features/fixes are merged to `main`
- Working tree is clean

**Do NOT use when:**
- There are uncommitted changes (`git status` not empty)
- Branch is not `main`
- Tag for the planned version already exists

## Quick Reference

| Step | Command |
|------|---------|
| Last tag | `git describe --tags --abbrev=0` |
| Commits since | `git log <last_tag>..HEAD --pretty=format:'%h %s'` |
| Build all | `make build-all VERSION=<X.Y.Z>` |
| Tag (annotated) | `git tag -a v<X.Y.Z> -m 'Release v<X.Y.Z>'` |
| Push | `git push origin main && git push origin v<X.Y.Z>` |
| Release | `gh release create v<X.Y.Z> dist/awg-server-* --notes-file <file>` |

## Workflow

### 1. Verify preconditions

```bash
test -z "$(git status --porcelain)" || { echo "uncommitted changes"; exit 1; }
[ "$(git rev-parse --abbrev-ref HEAD)" = "main" ] || { echo "not on main"; exit 1; }
LAST=$(git describe --tags --abbrev=0)
echo "Last release: $LAST"
```

### 2. Determine new version

If user did not specify, ask which bump type:

- **patch** (1.0.2 → 1.0.3) — bug fixes only
- **minor** (1.0.2 → 1.1.0) — new features, backwards-compatible
- **major** (1.0.2 → 2.0.0) — breaking API/protocol changes

### 3. Generate CHANGELOG entry

Read commits since last tag and translate each to a user-facing line.

```bash
git log $LAST..HEAD --pretty=format:'%h %s'
```

Categorize by conventional commit prefix; map to inline verbs in entries:

| Prefix | Verb | Skip if |
|--------|------|---------|
| `feat:` | "Added" | — |
| `fix:` | "Fixed" | — |
| `refactor:` | "Changed" | internal-only |
| `docs:` | "Changed" | not user-visible |
| `chore:` / `ci:` / `test:` | — | always skip |

Insert new section into `CHANGELOG.md` immediately above the previous `## [...]` heading. Match existing tone — see the `[1.0.2]` entry: imperative voice, references types/symbols in backticks, focuses on user-visible behavior, not implementation.

```markdown
## [X.Y.Z] - YYYY-MM-DD

- Added <feature> — <one-line user-visible description with `Symbols`>
- Fixed <bug> when <conditions>
- Changed <thing> to <new behavior>
```

### 4. Build, commit, tag, push

```bash
make build-all VERSION=X.Y.Z
test -d dist && ls dist/awg-server-* | wc -l   # expect 6 binaries
git add CHANGELOG.md
git commit -m "chore: release vX.Y.Z"
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin main
git push origin vX.Y.Z
```

### 5. Create GitHub release

Extract the section just added to `CHANGELOG.md` and use it as release notes.

```bash
awk -v ver="X.Y.Z" '
  /^## \[/ { if (in_section) exit; if ($0 ~ "\\["ver"\\]") { in_section=1; next } }
  in_section { print }
' CHANGELOG.md > /tmp/release-notes.md

gh release create vX.Y.Z dist/awg-server-* \
  --title "vX.Y.Z" \
  --notes-file /tmp/release-notes.md
```

Verify: `gh release view vX.Y.Z` shows the notes and 6 binary assets.

## Common Mistakes

- **Forgot `make build-all` before `gh release create`** → release published without binaries; clients can't `awg-server update`
- **Lightweight tag** (`git tag vX.Y.Z`) instead of annotated (`-a -m`) — `git describe` skips lightweight tags, breaks subsequent releases
- **Tagged HEAD before committing CHANGELOG** → tag points to wrong commit; `awg-server update` shows version without notes
- **Pushed `main` but forgot `git push <tag>`** — tag exists locally only; release create fails with "tag not found on remote"
- **Used `chore:` commits in CHANGELOG** — internal noise, hide from users
