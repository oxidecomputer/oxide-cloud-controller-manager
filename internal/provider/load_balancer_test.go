// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package provider

import (
	"testing"

	"github.com/oxidecomputer/oxide.go/oxide"
	v1 "k8s.io/api/core/v1"
)

func TestToLoadBalancerStatus(t *testing.T) {
	t.Run("WithInternalIPs", func(t *testing.T) {
		status := toLoadBalancerStatus(
			&oxide.FloatingIp{
				Ip: "203.0.113.10",
			},
			&v1.Node{
				Status: v1.NodeStatus{
					Addresses: []v1.NodeAddress{
						{
							Type:    v1.NodeInternalIP,
							Address: "10.0.0.10",
						},
					},
				},
			},
		)

		if len(status.Ingress) != 2 {
			t.Fatalf(
				"toLoadBalancerStatus returned %d ingress, want 2",
				len(status.Ingress),
			)
		}
		if status.Ingress[0].IP != "203.0.113.10" {
			t.Fatalf(
				"first ingress ip is %q, want %q",
				status.Ingress[0].IP, "203.0.113.10",
			)
		}
		if status.Ingress[0].IPMode == nil ||
			*status.Ingress[0].IPMode != v1.LoadBalancerIPModeProxy {
			t.Fatal(
				"first ingress IPMode should be Proxy",
			)
		}
		if status.Ingress[1].IP != "10.0.0.10" {
			t.Fatalf(
				"second ingress ip is %q, want %q",
				status.Ingress[1].IP, "10.0.0.10",
			)
		}
		if status.Ingress[1].IPMode != nil {
			t.Fatal(
				"second ingress IPMode should be nil",
			)
		}
	})

	t.Run("NoInternalIPs", func(t *testing.T) {
		status := toLoadBalancerStatus(
			&oxide.FloatingIp{
				Ip: "203.0.113.10",
			},
			nil,
		)

		if len(status.Ingress) != 1 {
			t.Fatalf(
				"toLoadBalancerStatus returned %d ingress, want 1",
				len(status.Ingress),
			)
		}
	})
}

func TestAddressAllocatorFromAnnotations(t *testing.T) {
	t.Run("NoAnnotations", func(t *testing.T) {
		alloc, err := addressAllocatorFromAnnotations(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if alloc.Type() != oxide.AddressAllocatorTypeAuto {
			t.Fatalf("type = %q, want %q",
				alloc.Type(), oxide.AddressAllocatorTypeAuto,
			)
		}
	})

	t.Run("ExplicitIP", func(t *testing.T) {
		alloc, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIP: "203.0.113.10",
			},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		explicit, ok := alloc.AsExplicit()
		if !ok {
			t.Fatal("expected explicit allocator")
		}
		if explicit.Ip != "203.0.113.10" {
			t.Fatalf("ip = %q, want %q",
				explicit.Ip, "203.0.113.10",
			)
		}
	})

	t.Run("ExplicitPool", func(t *testing.T) {
		alloc, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIPPool: "external",
			},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		auto, ok := alloc.AsAuto()
		if !ok {
			t.Fatal("expected auto allocator")
		}
		ps, ok := auto.PoolSelector.AsExplicit()
		if !ok {
			t.Fatal("expected explicit pool selector")
		}
		if string(ps.Pool) != "external" {
			t.Fatalf("pool = %q, want %q",
				ps.Pool, "external",
			)
		}
	})

	t.Run("IPVersion", func(t *testing.T) {
		alloc, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIPVersion: "v4",
			},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		auto, ok := alloc.AsAuto()
		if !ok {
			t.Fatal("expected auto allocator")
		}
		ps, ok := auto.PoolSelector.AsAuto()
		if !ok {
			t.Fatal("expected auto pool selector")
		}
		if ps.IpVersion != oxide.IpVersionV4 {
			t.Fatalf("ip_version = %q, want %q",
				ps.IpVersion, oxide.IpVersionV4,
			)
		}
	})

	t.Run("InvalidIPVersion", func(t *testing.T) {
		_, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIPVersion: "v5",
			},
		)
		if err == nil {
			t.Fatal("expected error for invalid ip version")
		}
	})

	t.Run("IPAndPoolMutuallyExclusive", func(t *testing.T) {
		_, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIP:     "203.0.113.10",
				AnnotationFloatingIPPool: "external",
			},
		)
		if err == nil {
			t.Fatal("expected error for mutually exclusive annotations")
		}
	})

	t.Run("PoolAndVersionMutuallyExclusive", func(t *testing.T) {
		_, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIPPool:    "external",
				AnnotationFloatingIPVersion: "v4",
			},
		)
		if err == nil {
			t.Fatal("expected error for mutually exclusive annotations")
		}
	})
}
