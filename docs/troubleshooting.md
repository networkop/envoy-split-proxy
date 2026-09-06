# Troubleshooting a broken bypass

Written after a full session spent chasing a routing fault from inside Envoy,
where it was not observable. Read the first section before touching anything.

## The one rule

**Envoy cannot see routing.** It binds a source address, sets a mark, connects,
and reports success. Whether the packet then left via the native interface or a
VPN tunnel is decided by the kernel afterwards, and nothing in Envoy's logs,
stats, or config dump reflects it.

So: if Envoy looks healthy but behaviour is wrong, the fault is **below** Envoy.
Get an external witness before reading a single Envoy log line.

### External witnesses, in order of preference

```bash
# 1. The remote server reporting the source IP it saw. Netflix OCAs include it
#    on every response. Nothing beats being told by the other end.
docker logs --since 3m envoy 2>&1 | grep -o 'addr=[0-9.]*' | sort -u

# 2. Ask the kernel what a rule decides. No traffic, no ambiguity.
ip route get 1.1.1.1 mark 0x51821    # bypassed -> native interface
ip route get 1.1.1.1                  # everything else -> tunnel

# 3. Compare exit addresses directly.
curl -s ifconfig.me                            # default path
curl -s --interface <BYPASS_IP> ifconfig.me    # source-bound path
```

If witness 1 shows an address you do not recognise, stop reading Envoy logs.

## Which layer is broken

| Layer | Owned by | Failure looks like | Check |
|---|---|---|---|
| Client app | the TV / browser | sticky; survives network changes; different errors on retry | restart the app |
| `iptables` REDIRECT | `-iptables`, or the host | no new connections appear in Envoy at all | `iptables -t nat -L PREROUTING -n` |
| Envoy matching | `split.yaml` | wrong cluster chosen, visible in logs | `grep "cluster 'envoy-split-proxy"` |
| Kernel routing | `ip rule` / `ip route` | **everything in Envoy looks perfect**, wrong exit IP | `ip route get … mark` |
| NAT | `MASQUERADE` on the tunnel | source silently rewritten, no error anywhere | witness 1 or 3 |

The bottom two rows are the dangerous ones: they produce no error at any layer
you control.

## Step 0: get a reliable repro

**Client state is sticky.** A failed session can stay failed regardless of what
you change underneath, so an unchanged symptom proves nothing until you have
restarted the client.

1. Restart the client (or force-quit and relaunch the app).
2. Reproduce.
3. Reproduce again to confirm it is deterministic, not intermittent.

Do this after **every** change below. Half a day was lost to results that were
just stale app state.

## The bisection ladder

Each rung removes one component. Restart the client between rungs.

| # | Configuration | Works ⇒ |
|---|---|---|
| 1 | Client → LAN gateway directly | the box is at fault (not the client, account, or title) |
| 2 | Client → box, VPN down, proxy on | the VPN/routing layer is at fault |
| 3 | Client → box, no REDIRECT rules | Envoy is at fault |
| 4 | 443 redirect only | the HTTP listener is at fault |
| 5 | 80 redirect only | the TCP proxy is at fault |

Rungs 1–3 are the valuable ones and take a minute each. Do not start reading
debug logs before they are done — the answer they give changes which logs are
worth reading.

Note rung 2 carefully: **with the tunnel down, the bypass is a no-op**, because
the default and bypass clusters then egress identically. That configuration can
only detect Envoy *mangling* traffic; it cannot detect a leak.

## Traps

Things that look like evidence and are not:

- **`/clusters` host lists are not traffic.** Both dynamic_forward_proxy
  clusters declare the same DNS cache (`name: "dns"`), so every resolved host is
  mirrored into *both* clusters whichever one actually used it. Use
  `upstream_cx_total` instead.
- **Stats are cumulative** across sessions. `curl -XPOST
  localhost:19000/reset_counters` before a test or the numbers mean nothing.
- **`curl --interface` does not test an fwmark rule.** It binds a source
  address. For mark-based rules use `ip route get … mark`.
- **`ip route get X from Y` requires Y to be a local address.** For forwarded
  traffic add `iif eth0`, otherwise you get `RTNETLINK answers: Invalid
  argument` and might read it as a fault.
- **A good reading can come from a bad window.** The VPN agent removes its rules
  on reconnect, so traffic briefly exits natively. A single healthy `addr=`
  sample may have been captured during one of those gaps — sample repeatedly.
- **Envoy's HTTP listener re-resolves the Host header**, so the upstream address
  can legitimately differ from the one the client dialled. Not a fault.
- **Another agent may delete your ip rule.** `smart-vpn-client` owns priority
  150 and, on every teardown or reconnect, deletes any rule it finds there plus
  the default route in the table that rule pointed at -- without checking the
  rule is its own. A bypass installed at 150 therefore survives until the next
  VPN reconnect and then vanishes, which looks exactly like it was never
  installed. `-rule-priority` defaults to 151 for this reason. Symptom: the app
  logs `Installed ip rule` at startup but `ip rule show` has nothing.
- **The host may already have its own source rule, above yours.** Synology DSM
  registers named tables in `/etc/iproute2/rt_tables` and installs
  `3: from <interface address> lookup eth0-table`, whose default is via the LAN
  gateway. Anything binding that address therefore leaves natively regardless of
  what you install at priority 150, and `ip route get <dst> from <address>` will
  report a healthy bypass even when your own rule is missing. Check the whole of
  `ip rule show`, not just your own priority.
- **`iptables` and `iptables-legacy` are different rulesets.** On Synology DSM
  the host's `iptables` is nft-backed and cannot open the `nat` table at all,
  failing with `No chain/target/match by that name` -- which reads like missing
  rules but is the wrong binary. `-iptables` uses `iptables-legacy` (as does
  smart-vpn-client, after the same detour), so inspect with:

  ```bash
  sudo iptables-legacy -t nat -L PREROUTING -n --line-numbers
  docker exec app iptables-legacy -t nat -L PREROUTING -n   # same netns
  ```

## Selector support by kernel version

The bypass needs an `ip rule` that matches only Envoy's upstream sockets. Which
selectors exist depends on the kernel and iproute2, and on older ones the source
address is the only option:

| Selector | Needs | Notes |
|---|---|---|
| `from <ip>` | any | Matches any socket that *binds* that address. Unbound sockets carry no source into the route lookup, so ordinary box traffic is unaffected. |
| `fwmark` | any kernel, plus `CAP_NET_ADMIN` **and the envoy process running as root** | `-bypass-mark`. The image's entrypoint runs `su-exec envoy` unless `ENVOY_UID=0`, and Docker cannot pass capabilities to an unprivileged process, so `--cap-add=NET_ADMIN` yields `EPERM` and `--user 0:0` does not help either. Set `-e ENVOY_UID=0`. Envoy **aborts the connection** when the option fails, so getting this wrong breaks every bypassed connection rather than degrading. `docker top envoy` shows the real uid; `docker exec envoy id` and `/proc/1/status` both report the entrypoint instead. |
| `uidrange` | kernel 4.10+, iproute2 4.10+ | The only true guarantee: a process cannot spoof its UID. Older iproute2 fails with `argument "uidrange" is wrong`. |
| `ipproto` | kernel 4.17+ | Narrows to TCP. |

Observed on a Synology DSM box: kernel 4.4.59, iproute2-ss160111 -- only `from`
is available. Verify what a host supports by adding a throwaway rule pointing at
an **empty** table, so a userland that accepts the rule but silently drops the
selector cannot divert anything:

```bash
sudo ip rule add uidrange 1337-1337 lookup 300 priority 151
ip rule show | grep 151      # the selector must appear in the output
sudo ip rule del prio 151
```

## Already ruled out — do not re-test

Established with evidence during the September 2026 investigation:

| Theory | Killed by |
|---|---|
| Missing Netflix domain in `split.yaml` | every SNI and `:authority` matched a `bypass-*` cluster |
| Default 15s route timeout | `upstream_rq_timeout: 0` on every cluster |
| Envoy mangling requests (headers, pipelining, dfproxy) | works with both listeners proxied when the VPN is down |
| DNS steering over the tunnel | pointing client DNS at the LAN gateway changed nothing |
| OCA outside the configured IP range | `default-http.upstream_cx_total: 1`, and that one was unrelated |
| Client plan / device eligibility | the same title plays when the client points at the LAN gateway |

The actual cause was that `UpstreamBindConfig` sets a source address but not a
route, and the VPN's priority-1000 catch-all swallowed the traffic. See the
README's "Host routing prerequisite".

## Start here

```bash
./scripts/selftest.sh
```

Covers routing, interception, and the actual egress address for both paths. If
it passes, the split is working and the fault is elsewhere.

## Fast checks, highest signal first

```bash
# is the bypass rule even installed?
ip rule show | grep -E '150|fwmark'
ip route get 1.1.1.1 mark 0x51821

# is the VPN agent reporting the bypass healthy? (smart-vpn-client)
curl -s localhost:2112/metrics | grep vpn_bypass

# what did the remote end actually see?
docker logs --since 3m envoy 2>&1 | grep -o 'addr=[0-9.]*' | sort -u

# how much traffic took each path? (reset first)
curl -XPOST localhost:19000/reset_counters
curl -s localhost:19000/stats | grep upstream_cx_total

# which cluster is chosen, and for what.
# Envoy 1.16's /logging takes ONE logger per request; multi-logger support came
# later, and passing several silently changes nothing but printing the usage.
curl -XPOST 'localhost:19000/logging?filter=debug'   # tcp_proxy + tls_inspector
curl -XPOST 'localhost:19000/logging?router=debug'   # http cluster selection
# ... reproduce, then put it back:
# curl -XPOST 'localhost:19000/logging?level=info'
docker logs --since 3m envoy 2>&1 | grep -o "requestedServerName: .*" | sort -u
docker logs --since 3m envoy 2>&1 | grep -o "cluster 'envoy-split-proxy-[a-z-]*'" | sort | uniq -c
```

`vpn_bypass_rule_present` exists precisely so this never needs a full
investigation again. Check it first.
