// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package loadbalancer

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	corev1 "k8s.io/api/core/v1"
)

const dynamicFilesystemConfig = `node:
  cluster: gardener-provider-local
  id: gardener-provider-local-id
dynamic_resources:
  cds_config:
    resource_api_version: V3
    path_config_source:
      path: /home/envoy/cds.yaml
  lds_config:
    resource_api_version: V3
    path_config_source:
      path: /home/envoy/lds.yaml
admin:
  access_log:
  - name: envoy.access_loggers.file
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog
      path: /dev/stdout
  address:
    socket_address:
      address: 0.0.0.0
      port_value: 10000
`

const ldsTemplate = `resources:
{{- range $key, $sp := .ServicePorts }}
- "@type": type.googleapis.com/envoy.config.listener.v3.Listener
  name: listener_{{ $key }}
  address:
    socket_address:
      address: 0.0.0.0
      port_value: {{ $sp.Listener.Port }}
      protocol: {{ $sp.Listener.Protocol }}
{{- if eq $sp.Listener.Protocol "UDP" }}
  udp_listener_config:
    downstream_socket_config:
      max_rx_datagram_size: 9000
  listener_filters:
  - name: envoy.filters.udp_listener.udp_proxy
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.udp.udp_proxy.v3.UdpProxyConfig
      stat_prefix: service
      matcher:
        on_no_match:
          action:
            name: route
            typed_config:
              "@type": type.googleapis.com/envoy.extensions.filters.udp.udp_proxy.v3.Route
              cluster: cluster_{{ $key }}
{{- else }}
  filter_chains:
  - filters:
    - name: envoy.filters.network.tcp_proxy
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
        stat_prefix: service
        cluster: cluster_{{ $key }}
{{- end }}
{{- end }}
`

const cdsTemplate = `resources:
{{- range $key, $sp := .ServicePorts }}
- "@type": type.googleapis.com/envoy.config.cluster.v3.Cluster
  name: cluster_{{ $key }}
  connect_timeout: 3s
  type: STATIC
  lb_policy: RANDOM
  common_lb_config:
    healthy_panic_threshold:
      value: 0
  health_checks:
  - timeout: 3s
    interval: 5s
    unhealthy_threshold: 3
    healthy_threshold: 1
    http_health_check:
      path: /healthz
      host: healthcheck
  load_assignment:
    cluster_name: cluster_{{ $key }}
    endpoints:
    - lb_endpoints:
{{- range $ep := $sp.Cluster }}
      - endpoint:
          health_check_config:
            port_value: 10256
          address:
            socket_address:
              address: {{ $ep.Address }}
              port_value: {{ $ep.Port }}
              protocol: {{ $ep.Protocol }}
{{- end }}
{{- end }}
`

var (
	ldsTemplateParsed = template.Must(template.New("lds").Parse(ldsTemplate))
	cdsTemplateParsed = template.Must(template.New("cds").Parse(cdsTemplate))
)

type proxyConfigData struct {
	ServicePorts map[string]servicePort
}

type servicePort struct {
	Listener endpoint
	Cluster  []endpoint
}

type endpoint struct {
	Address  string
	Port     int32
	Protocol string
}

func generateProxyConfig(service *corev1.Service, nodeIPs []string) (ldsConfig, cdsConfig []byte, err error) {
	data := &proxyConfigData{
		ServicePorts: make(map[string]servicePort),
	}

	for _, port := range service.Spec.Ports {
		if port.NodePort == 0 {
			continue
		}

		proto := strings.ToUpper(string(port.Protocol))
		if proto == "" {
			proto = "TCP"
		}

		key := fmt.Sprintf("%d_%s", port.Port, proto)

		sp := servicePort{
			Listener: endpoint{
				Address:  "0.0.0.0",
				Port:     port.Port,
				Protocol: proto,
			},
		}

		for _, nodeIP := range nodeIPs {
			sp.Cluster = append(sp.Cluster, endpoint{
				Address:  nodeIP,
				Port:     port.NodePort,
				Protocol: proto,
			})
		}

		data.ServicePorts[key] = sp
	}

	var ldsBuf, cdsBuf bytes.Buffer
	if err := ldsTemplateParsed.Execute(&ldsBuf, data); err != nil {
		return nil, nil, fmt.Errorf("error rendering LDS config: %w", err)
	}
	if err := cdsTemplateParsed.Execute(&cdsBuf, data); err != nil {
		return nil, nil, fmt.Errorf("error rendering CDS config: %w", err)
	}

	return ldsBuf.Bytes(), cdsBuf.Bytes(), nil
}
