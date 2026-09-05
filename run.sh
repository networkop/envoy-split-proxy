#!/bin/sh
#
# Brings up both containers on the box acting as the split-proxy router.
# Run from the directory holding split.yaml and envoy.yaml.

IMG=${IMG:-ghcr.io/networkop/envoy-split-proxy:latest}
ENVOY_IMG=${ENVOY_IMG:-envoyproxy/envoy:v1.16.2}

# How the host tells bypassed traffic apart from everything else.
#
#   0             Match on the source address Envoy binds. Works anywhere and
#                 needs no privileges, but also matches any other process that
#                 binds that address.
#
#   0x51821 etc.  Match on an fwmark Envoy sets via SO_MARK. Only sockets that
#                 deliberately set the mark qualify, so this is the precise
#                 option. It needs CAP_NET_ADMIN *and* the envoy process itself
#                 running as root -- the official image drops to uid 101 in its
#                 entrypoint (see 'docker top envoy'), and Docker cannot pass
#                 capabilities to an unprivileged process, so --user 0:0 is
#                 added below whenever a mark is configured.
#
#                 Envoy aborts the connection when a socket option cannot be
#                 applied, so a mark that fails to apply breaks every bypassed
#                 connection rather than degrading. The check at the end of this
#                 script catches that.
BYPASS_MARK=${BYPASS_MARK:-0}

if [ "$BYPASS_MARK" = "0" ]; then
  ENVOY_PRIV=""
else
  ENVOY_PRIV="--user 0:0 --cap-add=NET_ADMIN"
fi

docker pull "$IMG"
docker pull "$ENVOY_IMG"

docker rm -f app
docker rm -f envoy

# NET_ADMIN on app: -iptables manages the nat PREROUTING REDIRECT rules, and
# -ip-rule manages the policy route and ip rule. Between them nothing has to be
# configured on the host and nothing is lost across a reboot. -ip-rule follows
# -bypass-mark, installing an fwmark rule when one is set and a source rule
# otherwise, so the two flags stay consistent.
docker run -d --name app --restart always --net host --cap-add=NET_ADMIN \
-v $(pwd)/split.yaml:/split.yaml \
$IMG \
-conf /split.yaml -bypass-mark $BYPASS_MARK -iptables -ip-rule

docker run -d --name envoy --restart always --net host $ENVOY_PRIV \
-v $(pwd)/envoy.yaml:/etc/envoy/envoy.yaml \
$ENVOY_IMG \
--config-path /etc/envoy/envoy.yaml

# The app checks the routing itself at startup. Surface its verdict here so a
# silent bypass is visible without having to go looking for it.
sleep 3
echo
docker logs app 2>&1 | grep -i "bypass check" | tail -1

if [ "$BYPASS_MARK" != "0" ]; then
  failed=$(docker logs envoy 2>&1 | grep -c "socket option")
  if [ "$failed" -gt 0 ]; then
    echo
    echo "WARNING: Envoy could not apply SO_MARK ($failed occurrences)."
    echo "Every bypassed connection will fail. Confirm 'docker top envoy' shows"
    echo "the envoy process running as root, or fall back to BYPASS_MARK=0."
    echo
  fi
fi
