# Configuration Reference

All configuration is done via environment variables.

## Required

| Variable | Description | Example |
| -------- | ----------- | ------- |
| `AWG_API_TOKEN` | Bearer token for API auth | `my-secret-token-123` |
| `AWG_ADDRESS` | Server VPN address (CIDR) | `10.0.0.1/24` |
| `AWG_ENDPOINT` | Public IP/hostname for clients | `vpn.example.com` |

## Optional

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `AWG_LISTEN_PORT` | `51820` | WireGuard UDP listen port |
| `AWG_HTTP_PORT` | `7777` | HTTP API listen port |
| `AWG_MTU` | `1420` | MTU for client configs |
| `AWG_DNS` | `1.1.1.1` | DNS server for client configs |
| `AWG_DATA_DIR` | `/data` | Directory for clients.json persistence |

## AmneziaWG Obfuscation Parameters

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `AWG_JC` | `0` | Junk packet count |
| `AWG_JMIN` | `0` | Junk packet minimum size |
| `AWG_JMAX` | `0` | Junk packet maximum size |
| `AWG_S1` | `0` | Init packet padding |
| `AWG_S2` | `0` | Response packet padding |
| `AWG_S3` | `0` | Underload packet padding |
| `AWG_S4` | `0` | Transport packet padding |
| `AWG_H1` | `0` | Init packet header |
| `AWG_H2` | `0` | Response packet header |
| `AWG_H3` | `0` | Underload packet header |
| `AWG_H4` | `0` | Transport packet header |

Parameters with value `0` are omitted from client configs and `awg set` commands.

## Persistence

Client data is stored in `{AWG_DATA_DIR}/clients.json`:

```json
{
  "server_private_key": "<base64>",
  "clients": [
    {
      "id": "uuid",
      "name": "uuid",
      "private_key": "<base64>",
      "public_key": "<base64>",
      "address": "10.0.0.2",
      "created_at": "2026-01-01T00:00:00Z"
    }
  ]
}
```

On startup, all clients are restored and peers re-added to the device.
