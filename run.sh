#!/bin/sh
#
# Brings up both containers on the box acting as the split-proxy router.
# Run from the directory holding split.yaml and envoy.yaml.

IMG=${IMG:-ghcr.io/networkop/envoy-split-proxy:latest}
ENVOY_IMG=${ENVOY_IMG:-envoyproxy/envoy:v1.16.2}

# Must match the host's ip rule (see 'Host routing prerequisite' in README.md).
BYPASS_MARK=${BYPASS_MARK:-0x51821}

docker pull "$IMG"
docker pull "$ENVOY_IMG"

docker rm -f app
docker rm -f envoy

# NET_ADMIN on app: -iptables manages the nat PREROUTING REDIRECT rules.
docker run -d --name app --restart always --net host --cap-add=NET_ADMIN \
-v $(pwd)/split.yaml:/split.yaml \
$IMG \
-conf /split.yaml -bypass-mark $BYPASS_MARK -iptables

# NET_ADMIN on envoy: envoy is the process that sets SO_MARK on its own
# upstream sockets. app only serves the xDS config describing the option.
docker run -d --name envoy --restart always --net host --cap-add=NET_ADMIN \
-v $(pwd)/envoy.yaml:/etc/envoy/envoy.yaml \
$ENVOY_IMG \
--config-path /etc/envoy/envoy.yaml

# The mark is inert without a matching policy rule -- the bypass then fails
# silently, with healthy-looking logs and stats. Warn rather than mutate, since
# the routing setup is the host's business.
if ! ip rule show 2>/dev/null | grep -qi "fwmark $BYPASS_MARK"; then
  echo
  echo "WARNING: no ip rule matching fwmark $BYPASS_MARK."
  echo "Bypassed traffic will follow the host default route (e.g. a VPN tunnel)."
  echo "See 'Host routing prerequisite' in README.md. Roughly:"
  echo "  ip route replace default via <LAN_GATEWAY> dev <IFACE> table 200"
  echo "  ip rule add fwmark $BYPASS_MARK lookup 200 priority 150"
  echo
fi
