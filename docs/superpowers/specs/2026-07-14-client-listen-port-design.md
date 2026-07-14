# Client Listen Port Design

## Context

Generated client configurations currently let API callers override client-side MTU, DNS, and persistent keepalive behavior, but they cannot select the local UDP port bound by the client's AmneziaWG interface. WireGuard and AmneziaWG express that setting as `ListenPort` in the client configuration's `[Interface]` section.

The existing `awg_params.port` field has a different responsibility: it selects the UDP listen port of the server-side interface and becomes the port in the generated client `Endpoint`. The new setting must not change or alias that behavior.

## API Contract

Add an optional integer field named `client_listen_port` to `awg_params` for create and update requests.

- An omitted value or `0` selects automatic client-side port allocation. In this mode, the generated configuration omits `ListenPort`.
- Values from 1024 through 65535 inclusive produce `ListenPort = <value>` in the generated `[Interface]` section.
- Negative values, values from 1 through 1023, and values above 65535 return HTTP 400 before client state changes.
- Create, update, get, and list responses expose a nonzero stored value through the existing `awg_params` object.
- `PATCH` continues to replace the complete `awg_params` override object. Omitting `client_listen_port`, setting it to `0`, or setting `awg_params` to `null` returns the client to automatic selection.

The field is intentionally part of `awg_params` rather than the top-level client object because this API already groups client configuration overrides there. A plain integer is sufficient because there is no global client listen-port default that must be distinguished from explicit zero.

## Architecture and Data Flow

`internal/awg.AWGParams` owns the persisted JSON field and validation. The client manager merges a positive override into the effective parameters and renders it separately from CPS lines, alongside other client-only configuration settings.

`client_listen_port` is client-only. It must not:

- participate in `AWGParams.Key()` or server interface grouping;
- be passed to `awg set` on the server;
- change `awg_params.port` or the generated `Endpoint`;
- trigger peer migration or consume a server-side port.

Persistence requires no schema migration because existing JSON records simply omit the new optional field. Existing clients therefore retain automatic client-side port allocation until explicitly updated.

## Configuration Rendering

For a positive value, the generated configuration includes the setting in `[Interface]`:

```ini
[Interface]
PrivateKey = <base64>
ListenPort = 54321
Address = 10.0.0.2/32
```

When the effective value is zero, the `ListenPort` line is absent. No client-side firewall, NAT, or port-forwarding rules are managed by this server.

## Documentation and Verification

Update `docs/api.md`, `README.md`, and the shared architecture and security rules to distinguish `client_listen_port` from the existing server-side `port` field.

Per the explicit project requirement, no Go test files will be added. Verification will consist of formatting changed Go files, `go test ./...` as a package compilation check, `go vet ./...`, `go build -o awg-server .`, `git diff --check`, and final diff review.
