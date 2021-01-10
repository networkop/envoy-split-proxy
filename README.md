# envoy-split-proxy

Configure Envoy to act as a TCP proxy and SNI-based router to allow VPN bypass for VPN-sensitive applications like Netflix, BBC iPlayer, Amazon Prime etc. The assumption is that the host OS has multiple default routes and you want to steer _some_ traffic to a non-preferred default interface (the one that has higher metric). The current application will parse a [YAML file](./test.yaml) containing that non-default interface and a list of URLs and will configure Envoy to do SNI-based routing of these domains to that interface:

![](./arch.png)

The `envoy-split-proxy` process continues to run as an agent, monitoring all changes to the supplied configuration file and synchronizing the state with the Envoy proxy.

## Quickstart

On your client device, redirect all traffic to the box that will be running Envoy:

```
ip route add default via <IP_OF_ARM_BOX> metric 10
```

On the ARM box set up an iptables redirect to send all HTTPS traffic to envoy:

```
sudo iptables -t nat -A PREROUTING -p tcp --dport 443 -j REDIRECT --to-port 10000
```

Copy `envoy.yaml` and `test.yaml` into your `pwd` and run:

```
docker run --name envoy -d --net=host -v $(pwd)/envoy.yaml:/etc/envoy/envoy.yaml envoyproxy/envoy:v1.16.2 --config-path /etc/envoy/envoy.yaml

docker run --name app -d --net=host -v $(pwd)/test.yaml:/test.yaml networkop/envoy-split-proxy -conf /test.yaml
```

All traffic is now (L7-)transparently proxied by Envoy and all domains specified in `test.yaml` are redirected to the interface specificed.
