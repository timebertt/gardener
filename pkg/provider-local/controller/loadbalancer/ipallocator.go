// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package loadbalancer

import (
	"fmt"
	"net/netip"
	"sync"
)

// IPAllocator manages IP allocation from a CIDR range.
type IPAllocator struct {
	mu          sync.Mutex
	prefix      netip.Prefix
	services    map[string]netip.Addr
	reservedIPs []netip.Addr
}

// NewIPAllocator creates an allocator for the given CIDR.
func NewIPAllocator(cidr string, reservedIPs []netip.Addr) (*IPAllocator, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}

	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("only IPv4 CIDRs are supported, got %q", cidr)
	}

	return &IPAllocator{
		prefix:      prefix,
		services:    make(map[string]netip.Addr),
		reservedIPs: reservedIPs,
	}, nil
}

// Allocate returns an IP for the given service key, reusing desiredIP if provided.
func (a *IPAllocator) Allocate(serviceKey string, desiredIP string) (netip.Addr, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if desiredIP != "" {
		ip, err := netip.ParseAddr(desiredIP)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("invalid IP %q: %w", desiredIP, err)
		}

		if ip.Is4() {
			a.services[serviceKey] = ip
			return ip, nil
		}
	}

	if ip, found := a.nextFree(a.prefix); found {
		a.services[serviceKey] = ip
		return ip, nil
	} else {
		return netip.Addr{}, fmt.Errorf("no free IPs in CIDR %s", a.prefix)
	}
}

// Release frees the IP for the given service key.
func (a *IPAllocator) Release(serviceKey string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.services, serviceKey)
}

// Restore records an existing allocation from a previous run.
func (a *IPAllocator) Restore(serviceKey string, ip netip.Addr) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !ip.Is4() {
		return fmt.Errorf("not an IPv4 address: %q", ip)
	}

	if !a.prefix.Contains(ip) {
		return fmt.Errorf("IP %q is outside CIDR %q", ip, a.prefix)
	}

	a.services[serviceKey] = ip
	return nil
}

func (a *IPAllocator) isUsed(ip netip.Addr) bool {
	for _, v := range a.services {
		if v == ip {
			return true
		}
	}

	return false
}

func (a *IPAllocator) nextFree(prefix netip.Prefix) (netip.Addr, bool) {
	start := prefix.Addr()

	for ip := start; prefix.Contains(ip); ip = ip.Next() {
		if !a.isUsed(ip) {
			for _, reservedIP := range a.reservedIPs {
				if reservedIP != ip {
					return ip, true
				}
			}
		}
	}
	return netip.Addr{}, false
}
