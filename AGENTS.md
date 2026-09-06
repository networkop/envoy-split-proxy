# AGENTS.md

Guidance for AI coding agents working in this repository.

## What this project is

`envoy-split-proxy` is a small Go control plane (xDS server) for Envoy. It reads a
YAML file listing an "bypass" network interface plus a set of domain patterns, and
programs a running Envoy so that traffic to those domains egresses from that
interface instead of the host's preferred default route. The typical use case is
bypassing a VPN for services like Netflix / iPlayer / Prime Video.

The Go binary never proxies traffic itself — Envoy does. The binary only serves
CDS/LDS over gRPC and keeps the snapshot in sync with the config file.

## Layout

| Path | Purpose |
|---|---|
| `main.go` | Entry point; calls `cmd.Run()`. |
| `cmd/main.go` | Flag parsing, wires the config watcher to the Envoy controller via `chan *config.Data`. |
| `pkg/config/parser.go` | Parses `split.yaml`, resolves the interface to its first IPv4 address via netlink, dedups URLs, does idempotent state updates. |
| `pkg/config/watcher.go` | fsnotify watch on the config file; re-parses and pushes changed state onto the channel. |
| `pkg/envoy/main.go` | Builds the xDS snapshot (2 listeners, 4 clusters) and serves it over gRPC. |
| `pkg/envoy/envoy_test.go` | Unit tests for the SNI/vhost domain rules and the bypass bind config. |
| `pkg/iptables/redirect.go` | Optional (`-iptables`) management of the nat PREROUTING REDIRECT rules; idempotent via `-C`, removed on SIGTERM. Shells out, so the image is alpine rather than distroless. |
| `pkg/route/policy.go` | Optional (`-ip-rule`) management of the policy route and `ip rule` that steer bypassed traffic; gateway derived from the main table's default via the bypass link. |
| `pkg/route/verify.go` | Startup check (`-verify`, on by default) that the host really does steer bypassed traffic out the bypass interface. Runs regardless of who installed the rules. |
| `envoy.yaml` | Envoy bootstrap: node id `split`, dynamic CDS/LDS pointing at `127.0.0.1:18000`, admin on `:19000`. |
| `split.yaml` | Example user config (`interface:` + `urls:`). |
| `docker-compose.yaml`, `run.sh` | Local/dev run helpers. |
| `scripts/selftest.sh` | End-to-end check run on the box: routing, REDIRECT rules, listeners, and the real egress address for both paths. Exits non-zero on failure. |
| `.github/workflows/` | `ci.yml` (lint + test), `docker-publish.yml` (multi-arch push to ghcr.io). `docker.yml` is intentionally disabled. |

## Architecture notes an agent must know

- **xDS v2, not v3.** `go-control-plane` is pinned at v0.9.8 and the code imports
  `envoy/api/v2`, `pkg/cache/v2`, `pkg/server/v2`. Envoy is pinned to **v1.16.2**
  because later Envoy releases removed v2 xDS. Do not "upgrade" the imports to v3
  without also bumping the Envoy image everywhere (`README.md`, `run.sh`,
  `docker-compose.yaml`) — they must move together.
- **Two listeners are generated:**
  - HTTPS (`-https-port`, default 10000): TCP proxy with `use_original_dst`. The
    first filter chain matches on SNI (`FilterChainMatch.ServerNames`) for bypass
    domains; the second is the catch-all default chain.
  - HTTP (`-http-port`, default 10001): HTTP connection manager with a
    dynamic_forward_proxy filter; bypass domains become a separate virtual host.
- **Four clusters:** default/bypass × TLS/HTTP. The bypass clusters differ only in
  their `UpstreamBindConfig`: the source address is set to the bypass interface
  IP, and `SO_MARK` is set to `-bypass-mark` (default `0x51821`, 0 disables) via
  `SocketOptions` at `STATE_PREBIND`. Neither picks a route — the host needs an
  `ip rule` matching that mark, or the bypass silently does nothing. See the
  README's "Host routing prerequisite" and `docs/vpn-agent-integration.md`.
  `SO_MARK` is applied by Envoy to its own upstream sockets, so the privileges
  belong on the **envoy** container, not on this control plane -- and
  `--cap-add=NET_ADMIN` alone is not enough, because the image's entrypoint runs
  `su-exec envoy` unless `ENVOY_UID=0` is set and Docker cannot pass
  capabilities to an unprivileged process. `-e ENVOY_UID=0` is required;
  `--user 0:0` does not help. The mark defaults to 0 because Envoy aborts the
  connection when a socket option cannot be applied, so getting this wrong
  breaks every bypassed connection instead of degrading.
- **Host header ports.** Envoy 1.16 matches virtual host domains against the raw
  `:authority`, and `strip_matching_host_port` does not exist in the v2 HCM API.
  Clients that send the default port (the Netflix LG TV app does, for its Pushy
  websocket `nrdp.push.prod.netflix.com:80`) would miss every domain entry, so
  `withDefaultPort` pairs each configured URL with a `:80` form for the HTTP
  virtual host only. SNI values never carry a port, so the TLS chain is unaffected.
- **`excludePartialWildCards`** exists because Envoy rejects SNI matches that are
  not valid there: partial wildcards (`*-bar.foo.com`), bare `*`, and IP-shaped
  entries (`81.130.98.*`, `192.168.1.1`), which can never be an SNI value. Exact
  hostnames and full leading-label wildcards (`*.netflix.com`) are kept. The
  predicate lives in `validServerName`; changing it needs a matching test case.
- **Interface resolution uses netlink** (`vishvananda/netlink` + `unix.AF_INET`),
  so parsing only works on Linux and requires the interface to exist with at least
  one IPv4 address. This is why the parser is not covered by unit tests.
- `Data.Changed` is set by the parser and cleared by `Envoy.Configure` after a
  successful `SetSnapshot`; the snapshot version is `time.Now().String()`.

## Build, test, lint

```
make                 # CGO_ENABLED=0 go build -o envoy-split-proxy .
make test            # go test -race ./... -v
make lint            # golangci-lint run  (CI uses v1.59)
make docker          # buildx multi-arch push; override DOCKER_IMAGE=
```

Go toolchain is **1.22.x** (`go.mod`, Dockerfile, CI). Keep those in sync.

Run locally:

```
./envoy-split-proxy -conf split.yaml -debug
```

Verify what Envoy actually received:

```
curl localhost:19000/config_dump | jq '.configs[2]'
```

## Conventions

- Logging is `logrus`; `logrus.Infof` for operator-visible events, `logrus.Debugf`
  for anything verbose. `-debug` flag flips the level. Errors from the config
  watcher are logged, not fatal — the previous good config is retained.
- Errors returned from parsing are wrapped with `fmt.Errorf` and plain context
  strings; keep the existing style rather than introducing a new error package.
- No external test deps — plain `testing` with table-driven subtests.
- The published image is `ghcr.io/networkop/envoy-split-proxy`. Older docs may
  still reference Docker Hub (`networkop/...`); prefer ghcr.io for new references.

## Debugging a broken bypass

**Read [docs/troubleshooting.md](docs/troubleshooting.md) before investigating a
routing or bypass fault.** The short version: Envoy cannot observe routing, so a
healthy-looking Envoy proves nothing about which interface packets left by. Get
an external witness (`addr=` in OCA responses, `ip route get ... mark`) before
reading Envoy logs, and work the bisection ladder in that document rather than
guessing at config. It also lists theories already eliminated with evidence, so
they do not get re-tested.

## Gotchas

- Root `main.go` and `cmd/main.go` are both `package main`/`package cmd` — the
  build target is the repo root (`go build .`), not `./cmd`.
- `pkg/envoy/main.go` shadows the package-level cluster-name vars with local
  `*api.Cluster` values inside `buildCluster`. Be careful when editing that
  function that string names and cluster structs aren't confused.
- Changing flags in `cmd/main.go` requires updating `README.md` and the iptables
  REDIRECT ports documented there (10000 / 10001).
- Do not commit the built `envoy-split-proxy` binary — it is gitignored.

## Planned work

**Migrate to xDS v3.** Not urgent, but Envoy 1.16.2 is from 2021 and unsupported,
and v2 was removed from Envoy after that release, so the pin cannot move until
this happens. It is an API migration, not a version bump:

- `go-control-plane` v0.9.8 -> v0.13.x; imports move from `envoy/api/v2`,
  `pkg/cache/v2` and `pkg/server/v2` to `config/{listener,cluster,route}/v3`,
  `pkg/cache/v3`, `pkg/server/v3`.
- `envoy.yaml` needs `transport_api_version: V3` and `resource_api_version: V3`.
- **The trap:** `use_original_dst` and the implicit TLS inspector are gone in v3.
  Both listener filters must be declared explicitly
  (`envoy.filters.listener.original_dst`, `.tls_inspector`). Miss either and SNI
  matching silently stops working, which looks exactly like the routing bug that
  `-bypass-mark` exists to prevent -- verify with a `config_dump` diff against
  the v2 output, not just by checking the process starts.
- Bump the Envoy image in `README.md`, `run.sh` and `docker-compose.yaml`
  together.
- `strip_matching_host_port` exists on the v3 HTTP connection manager, so
  `withDefaultPort` can be dropped once this lands.
