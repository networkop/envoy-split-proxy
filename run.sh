docker run --name envoy -d \
--net=host \
-v /var/services/homes/admin/envoy.yaml:/etc/envoy/envoy.yaml \
envoyproxy/envoy:v1.16.2 \
--config-path /etc/envoy/envoy.yaml \
--log-level debug
