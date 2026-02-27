# fortivpn (Go)

CLI for FortiClient VPN on macOS with profile-level control (for example `prod` / `int`).

It uses FortiClient's native bridge API (`guimessenger_jyp.node`) so you can list and connect specific FortiClient connections, not just the single macOS network service.

## Prerequisites

- Go must be installed (`go` command available in your shell).
- Install guide: https://go.dev/doc/install
- Node.js must be installed (`node` command available in your shell).

## Build

```bash
go build -o fortivpn .
```

## Usage

```bash
./fortivpn connections
./fortivpn status --connection prod
./fortivpn connect --connection int
./fortivpn connect --connection prod
./fortivpn watch --connection prod --interval 10
```

## Commands

- `connections`: list available FortiClient VPN connections (profiles)
- `status`: print current connection status
- `connect`: idempotent connect to a chosen connection
- `disconnect`: disconnect active VPN connection
- `watch`: monitor and auto-connect to the chosen connection

## Helpful Flags

- `--connection <name>`: choose connection by name; partials like `prod` or `int` are supported when unambiguous
- `--json`: machine-readable output
- `--timeout <sec>`: wait timeout for connection transitions
- `--interval <sec>`: polling interval

## Notes

- `connect` is idempotent: if already connected to the selected connection, it exits successfully without reconnecting.
- If already connected to a different connection, `connect --connection ...` asks FortiClient to switch directly (it does not force a pre-disconnect).
- `connect` will auto-start the FortiClient app if it is not running.
- If FortiClient requires MFA or interactive SAML authentication, connect may still require user interaction.
