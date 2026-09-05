#!/bin/sh
#
# End-to-end check of the split proxy. Run on the box, after a reboot or after
# changing split.yaml, the ip rules, or the containers.
#
# The startup check inside envoy-split-proxy only asks the kernel which route it
# would pick. This exercises the real data path: that Envoy is listening, that
# split.yaml matching selects the right cluster, and -- by reading the address a
# remote server reports back -- which interface the connection actually left by.
#
# Exit status is 0 only if every check passes, so it works as a cron or
# healthcheck probe.

HTTP_PORT=${HTTP_PORT:-10001}
HTTPS_PORT=${HTTPS_PORT:-10000}
ADMIN_PORT=${ADMIN_PORT:-19000}
SPLIT=${SPLIT:-./split.yaml}
APP_CONTAINER=${APP_CONTAINER:-app}

# Must be listed in split.yaml (it is, under "## Testing") and must return the
# caller's IP as plain text over HTTP.
BYPASS_HOST=${BYPASS_HOST:-ifconfig.me}
# Must NOT match anything in split.yaml.
DEFAULT_HOST=${DEFAULT_HOST:-icanhazip.com}

pass=0
fail=0

ok()   { echo "  ok    $1"; pass=$((pass+1)); }
bad()  { echo "  FAIL  $1"; fail=$((fail+1)); }
info() { echo "  --    $1"; }

echo "== configuration =="

IFACE=${IFACE:-$(awk '/^interface:/{print $2}' "$SPLIT" 2>/dev/null)}
if [ -z "$IFACE" ]; then
  echo "  FAIL  cannot read 'interface:' from $SPLIT (set IFACE=... to override)"
  exit 1
fi

BYPASS_IP=${BYPASS_IP:-$(ip -4 -o addr show dev "$IFACE" 2>/dev/null | awk 'NR==1{split($4,a,"/"); print a[1]}')}
if [ -z "$BYPASS_IP" ]; then
  echo "  FAIL  no IPv4 address on $IFACE"
  exit 1
fi
info "bypass interface $IFACE, address $BYPASS_IP"

echo "== host routing =="

if ip rule show 2>/dev/null | grep -q "from $BYPASS_IP"; then
  ok "ip rule selects on $BYPASS_IP"
else
  bad "no ip rule selects on $BYPASS_IP -- bypassed traffic follows the default route"
fi

# No packets are sent; this only asks which route the kernel would choose.
bypass_dev=$(ip route get 1.1.1.1 from "$BYPASS_IP" 2>/dev/null | sed -n 's/.* dev \([^ ]*\).*/\1/p' | head -1)
default_dev=$(ip route get 1.1.1.1 2>/dev/null | sed -n 's/.* dev \([^ ]*\).*/\1/p' | head -1)

if [ "$bypass_dev" = "$IFACE" ]; then
  ok "traffic from $BYPASS_IP routes via $IFACE"
else
  bad "traffic from $BYPASS_IP routes via '${bypass_dev:-?}', expected $IFACE"
fi

if [ -z "$default_dev" ]; then
  bad "no default route at all"
elif [ "$default_dev" = "$IFACE" ]; then
  info "everything else also routes via $IFACE -- no separate default path, so the split is a no-op (VPN down?)"
else
  ok "everything else routes via $default_dev"
fi

echo "== interception =="

# DSM's nft-backed iptables cannot open the nat table at all, so prefer the
# legacy binary; looking with the wrong one makes present rules appear missing.
IPT=""
for candidate in /sbin/iptables-legacy /usr/sbin/iptables-legacy iptables-legacy iptables; do
  if command -v "$candidate" >/dev/null 2>&1; then IPT="$candidate"; break; fi
done

ipt_out=""
ipt_via=""
if [ -n "$IPT" ]; then
  ipt_out=$($IPT -t nat -L PREROUTING -n 2>&1) && ipt_via="$IPT"
fi

# Falling back to the app container covers two cases at once: reading the nat
# table needs root, and on DSM the host's nft-backed iptables cannot open it at
# all. The container ships iptables-legacy and shares the host network
# namespace, so it sees exactly the same rules.
if [ -z "$ipt_via" ] && command -v docker >/dev/null 2>&1; then
  ipt_out=$(docker exec "$APP_CONTAINER" iptables-legacy -t nat -L PREROUTING -n 2>&1) \
    && ipt_via="$APP_CONTAINER:iptables-legacy"
fi

if [ -z "$ipt_via" ]; then
  info "cannot read nat PREROUTING (tried ${IPT:-no local binary}, then container $APP_CONTAINER)"
else
  rules=$(echo "$ipt_out" | grep -c REDIRECT)
  if [ "$rules" -gt 0 ]; then
    ok "$rules REDIRECT rule(s) in nat PREROUTING (via $ipt_via)"
  else
    bad "no REDIRECT rules in nat PREROUTING (via $ipt_via) -- nothing reaches Envoy"
  fi
fi

# Ask Envoy which listeners it has rather than probing the ports. The https
# listener uses use_original_dst, so a connection from localhost resolves to
# Envoy itself and can never succeed -- probing it proves nothing either way.
listeners=$(curl -s --max-time 5 "http://127.0.0.1:$ADMIN_PORT/listeners" 2>/dev/null)
if [ -z "$listeners" ]; then
  bad "Envoy admin on port $ADMIN_PORT is not responding -- is the envoy container running?"
  envoy_up=0
else
  envoy_up=1
  for port in "$HTTPS_PORT" "$HTTP_PORT"; do
    if echo "$listeners" | grep -q ":$port\$\|:$port[^0-9]"; then
      ok "Envoy has a listener on port $port"
    else
      bad "Envoy has no listener on port $port"
    fi
  done
fi

echo "== egress addresses =="

# Reference addresses, taken without going through Envoy. --interface binds the
# source address the way Envoy's bypass clusters do.
direct=$(curl -s --max-time 10 --interface "$BYPASS_IP" "http://$BYPASS_HOST" | tr -d '[:space:]')
vpn=$(curl -s --max-time 10 "http://$BYPASS_HOST" | tr -d '[:space:]')

if [ -z "$direct" ] || [ -z "$vpn" ]; then
  bad "could not reach $BYPASS_HOST to establish reference addresses"
elif [ "$direct" = "$vpn" ]; then
  info "both paths exit as $direct -- nothing to distinguish (VPN down?)"
else
  ok "reference addresses differ: bypass $direct, default $vpn"
fi

echo "== through envoy =="

if [ "$envoy_up" != "1" ]; then
  info "skipped, Envoy is not reachable"
  echo
  echo "$pass passed, $fail failed"
  [ "$fail" -eq 0 ]
  exit
fi

# The HTTP listener routes on the Host header via dynamic_forward_proxy, so it
# can be driven directly. The response body is the address the remote server
# saw, which is the only thing that proves which interface was used.
got_bypass=$(curl -s --max-time 10 -H "Host: $BYPASS_HOST" "http://127.0.0.1:$HTTP_PORT/" | tr -d '[:space:]')
got_default=$(curl -s --max-time 10 -H "Host: $DEFAULT_HOST" "http://127.0.0.1:$HTTP_PORT/" | tr -d '[:space:]')

if [ -n "$direct" ] && [ "$got_bypass" = "$direct" ]; then
  ok "$BYPASS_HOST via Envoy exits as $got_bypass (bypassed)"
else
  bad "$BYPASS_HOST via Envoy exits as '${got_bypass:-?}', expected $direct -- check it is listed in $SPLIT"
fi

if [ -n "$vpn" ] && [ "$got_default" = "$vpn" ]; then
  ok "$DEFAULT_HOST via Envoy exits as $got_default (default path)"
else
  bad "$DEFAULT_HOST via Envoy exits as '${got_default:-?}', expected $vpn -- is it matching a split.yaml entry?"
fi

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
