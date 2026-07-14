# Code Style

## Go Conventions

- Minimize dependencies and prefer established project patterns
- Early returns over deep nesting
- Vertical spacing between variable declarations
- Error wrapping with `fmt.Errorf("context: %w", err)`
- No comments unless logic is non-obvious

## Naming

- Packages: lowercase, single word (`awg`, `clients`, `config`, `api`)
- Files: `kebab-case.go` for multi-word, `single.go` for single
- Types: `PascalCase` (exported), `camelCase` (unexported)
- Functions: `PascalCase` for exported, `camelCase` for internal

## Error Handling

- Always wrap errors with context
- Return errors up, log at boundary (main, HTTP handlers)
- Use `log.Printf` for warnings, `log.Fatalf` for fatal startup errors
