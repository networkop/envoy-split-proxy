#!/bin/sh
#
# Brings up both containers on the box acting as the split-proxy router.
# Run from the directory holding split.yaml and envoy.yaml.

IMG=${IMG:-ghcr.io/networkop/envoy-split-proxy:latest}
ENVOY_IMG=${ENVOY_IMG:-envoyproxy/envoy:v1.16.2}

# Address Envoy binds bypassed upstream sockets to. Only used to check the host
# policy rule below; Envoy derives it from split.yaml's `interface`.
BYPASS_IP=${BYPASS_IP:-}

# 0 steers by source address alone, which works anywhere.
#
# Setting a mark (e.g. 0x51821) is narrower -- only Envoy's bypass sockets carry
# it -- but Envoy aborts the connection when a socket option cannot be applied,
# so on a host that refuses SO_MARK this breaks every bypassed connection.
#
# Known not to work on Synology DSM even with root + CAP_NET_ADMIN +
# --privileged, though the same mark works from the host shell. --privileged is
# still added below when a mark is set, since it is required on some hosts.
# Verify before enabling: docker logs envoy 2>&1 | grep -c 'socket option'
BYPASS_MARK=${BYPASS_MARK:-0}

if [ "$BYPASS_MARK" = "0" ]; then
  ENVOY_PRIV=""
  MATCH="from ${BYPASS_IP:-<BYPASS_IP>}"
else
  ENVOY_PRIV="--privileged"
  MATCH="fwmark $BYPASS_MARK"
fi

docker pull "$IMG"
docker pull "$ENVOY_IMG"

docker rm -f app
docker rm -f envoy

# NET_ADMIN on app: -iptables manages the nat PREROUTING REDIRECT rules.
docker run -d --name app --restart always --net host --cap-add=NET_ADMIN \
-v $(pwd)/split.yaml:/split.yaml \
$IMG \
-conf /split.yaml -bypass-mark $BYPASS_MARK -iptables -ip-rule

# Envoy is the process that sets SO_MARK on its own upstream sockets, so the
# privileges for that belong here rather than on app, which only serves the xDS
# config describing the option.
docker run -d --name envoy --restart always --net host --cap-add=NET_ADMIN $ENVOY_PRIV \
-v $(pwd)/envoy.yaml:/etc/envoy/envoy.yaml \
$ENVOY_IMG \
--config-path /etc/envoy/envoy.yaml

# -ip-rule installs the policy route and rule, so confirm it actually landed.
# Without them the bypass fails silently, with healthy-looking logs and stats.
sleep 2
if ! ip rule show 2>/dev/null | grep -q "lookup 200"; then
  echo
  echo "WARNING: no ip rule found at priority 150 pointing at table 200."
  echo "Bypassed traffic will follow the host default route (e.g. a VPN tunnel)."
  echo "Check:  docker logs app 2>&1 | grep -i 'ip rule'"
  echo
fi
