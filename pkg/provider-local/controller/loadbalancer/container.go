// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package loadbalancer

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
)

const (
	containerPrefix    = "gardener-lb-"
	labelRoleKey       = "gardener.cloud/role"
	labelRoleValue     = "loadbalancer"
	labelServiceKey    = "gardener.cloud/service"
	envoyAdminPort     = 10000
	envoyReadyTimeout  = 2 * time.Minute
	envoyReadyInterval = time.Second
	emptyXDSConfig = "resources: []\n"
)

func containerName(serviceKey string) string {
	hash := sha256.Sum256([]byte(serviceKey))
	return fmt.Sprintf("%s%x", containerPrefix, hash[:6])
}

func newDockerClient() (*dockerclient.Client, error) {
	return dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
}

func ensureContainer(ctx context.Context, log logr.Logger, docker *dockerclient.Client, service *corev1.Service, lbIP, networkName, envoyImage string) (string, error) {
	serviceKey := fmt.Sprintf("%s/%s", service.Namespace, service.Name)
	name := containerName(serviceKey)

	info, err := docker.ContainerInspect(ctx, name)
	if err == nil {
		if info.State.Running {
			return name, nil
		}
		log.Info("Container exists but is not running, recreating", "container", name)
		if err := docker.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil {
			return "", fmt.Errorf("failed to remove stopped container %s: %w", name, err)
		}
	} else if !cerrdefs.IsNotFound(err) {
		return "", fmt.Errorf("failed to inspect container %s: %w", name, err)
	}

	log.Info("Creating proxy container", "container", name, "serviceKey", serviceKey)

	portBindings := nat.PortMap{}
	for _, port := range service.Spec.Ports {
		proto := strings.ToLower(string(port.Protocol))
		if proto == "" {
			proto = "tcp"
		}
		cp := nat.Port(fmt.Sprintf("%d/%s", port.Port, proto))
		portBindings[cp] = []nat.PortBinding{{HostIP: lbIP, HostPort: fmt.Sprintf("%d", port.Port)}}
	}

	resp, err := docker.ContainerCreate(ctx,
		&container.Config{
			Hostname: name,
			Image:    envoyImage,
			Cmd:      []string{"envoy", "-c", "/home/envoy/envoy.yaml"},
			Labels:   map[string]string{labelRoleKey: labelRoleValue, labelServiceKey: serviceKey},
		},
		&container.HostConfig{
			PortBindings:  portBindings,
			NetworkMode:   container.NetworkMode(networkName),
			Privileged:    true,
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyOnFailure},
			Sysctls:       map[string]string{"net.ipv4.ip_forward": "1"},
		},
		nil, nil, name,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container %s: %w", name, err)
	}

	if err := copyFilesToContainer(ctx, docker, resp.ID, "/home/envoy/", map[string][]byte{
		"envoy.yaml": []byte(dynamicFilesystemConfig),
		"cds.yaml":   []byte(emptyXDSConfig),
		"lds.yaml":   []byte(emptyXDSConfig),
	}); err != nil {
		return "", fmt.Errorf("failed to seed config in %s: %w", name, err)
	}

	if err := docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start container %s: %w", name, err)
	}
	return name, nil
}

func deleteContainer(ctx context.Context, log logr.Logger, docker *dockerclient.Client, serviceKey string) error {
	name := containerName(serviceKey)
	log.Info("Deleting proxy container", "container", name)
	err := docker.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

func updateContainerConfig(ctx context.Context, log logr.Logger, docker *dockerclient.Client, name string, ldsConfig, cdsConfig []byte) error {
	log.V(1).Info("Updating envoy config", "container", name)

	if err := copyFilesToContainer(ctx, docker, name, "/home/envoy/", map[string][]byte{
		"cds.yaml": cdsConfig,
		"lds.yaml": ldsConfig,
	}); err != nil {
		return fmt.Errorf("failed to write config to %s: %w", name, err)
	}

	return waitContainerReady(ctx, log, docker, name)
}

func copyFilesToContainer(ctx context.Context, docker *dockerclient.Client, containerID, destDir string, files map[string][]byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			return err
		}
		if _, err := tw.Write(content); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return docker.CopyToContainer(ctx, containerID, destDir, &buf, container.CopyToContainerOptions{})
}

func waitContainerReady(ctx context.Context, log logr.Logger, docker *dockerclient.Client, name string) error {
	ctx, cancel := context.WithTimeout(ctx, envoyReadyTimeout)
	defer cancel()

	info, err := docker.ContainerInspect(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to inspect container %s: %w", name, err)
	}

	var containerIP string
	for _, nw := range info.NetworkSettings.Networks {
		if nw.IPAddress != "" {
			containerIP = nw.IPAddress
			break
		}
	}
	if containerIP == "" {
		return fmt.Errorf("container %s has no IP address", name)
	}

	readyURL := fmt.Sprintf("http://%s:%d/ready", containerIP, envoyAdminPort)
	httpClient := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(envoyReadyInterval)
	defer ticker.Stop()

	for {
		if resp, err := httpClient.Get(readyURL); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				log.V(1).Info("Envoy container ready", "container", name)
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for envoy container %s to become ready", name)
		case <-ticker.C:
		}
	}
}

func garbageCollectContainers(ctx context.Context, log logr.Logger, docker *dockerclient.Client, serviceKeys map[string]bool) error {
	containers, err := docker.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelRoleKey+"="+labelRoleValue)),
	})
	if err != nil {
		return fmt.Errorf("failed listing proxy containers: %w", err)
	}

	for _, c := range containers {
		svcKey := c.Labels[labelServiceKey]
		if !serviceKeys[svcKey] {
			log.Info("Garbage collecting orphaned proxy container", "container", c.ID[:12], "serviceKey", svcKey)
			if err := docker.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				log.Error(err, "Failed to remove orphaned container", "container", c.ID[:12])
			}
		}
	}
	return nil
}
