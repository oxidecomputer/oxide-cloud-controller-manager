// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/oxidecomputer/oxide.go/oxide"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	cloudprovider "k8s.io/cloud-provider"
)

const (
	// AnnotationFloatingIP specifies an explicit IP address to allocate for
	// the floating IP, using the default IP pool for the respective IP version
	// of the address. Mutually exclusive with [AnnotationFloatingIPPool] and
	// [AnnotationFloatingIPVersion].
	AnnotationFloatingIP = "oxide.computer/floating-ip"

	// AnnotationFloatingIPPool specifies the IP pool to automatically allocate a
	// floating IP from.
	AnnotationFloatingIPPool = "oxide.computer/floating-ip-pool"

	// AnnotationFloatingIPVersion specifies the IP version (e.g., `v4` or `v6`) of
	// the floating IP to allocate, using the default IP pool for the IP version.
	// Cannot be used when [AnnotationFloatingIPPool] is set.
	AnnotationFloatingIPVersion = "oxide.computer/floating-ip-version"
)

var _ cloudprovider.LoadBalancer = (*LoadBalancer)(nil)

// LoadBalancer implements [cloudprovider.LoadBalancer] by attaching a
// single floating IP to one Kubernetes node.
type LoadBalancer struct {
	client    *oxide.Client
	project   string
	k8sClient kubernetes.Interface
}

// GetLoadBalancer returns the status of the floating IP "load balancer" for
// the given service. It fetches the floating IP from Oxide, checks whether
// the floating IP is attached to an instance that's a valid Kubernetes node,
// and returns the load balancer status with the floating IP address and the
// instance's internal IP addresses.
func (l *LoadBalancer) GetLoadBalancer(
	ctx context.Context,
	clusterName string,
	service *v1.Service,
) (*v1.LoadBalancerStatus, bool, error) {
	floatingIPName := l.GetLoadBalancerName(ctx, clusterName, service)

	floatingIP, err := l.client.FloatingIpView(
		ctx, oxide.FloatingIpViewParams{
			FloatingIp: oxide.NameOrId(floatingIPName),
			Project:    oxide.NameOrId(l.project),
		},
	)
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf(
			"failed viewing floating ip: %w", err,
		)
	}

	// This floating IP isn't attached to an instance so we skip adding the node's
	// internal IP addresses to the load balancer status.
	if floatingIP.InstanceId == "" {
		return toLoadBalancerStatus(floatingIP, nil), true, nil
	}

	// Fetch all the Kubernetes nodes.
	nodes, err := l.k8sClient.CoreV1().Nodes().List(
		ctx, metav1.ListOptions{},
	)
	if err != nil {
		return nil, false, fmt.Errorf(
			"failed listing kubernetes nodes: %w", err,
		)
	}

	// Find the Kubernetes node that the floating IP is attached to.
	providerID := NewProviderID(floatingIP.InstanceId)
	index := slices.IndexFunc(nodes.Items, func(node v1.Node) bool {
		return node.Spec.ProviderID == providerID
	})
	if index == -1 {
		return nil, false, fmt.Errorf(
			"floating ip attached to non-cluster instance %q", floatingIP.InstanceId,
		)
	}

	return toLoadBalancerStatus(
		floatingIP, &nodes.Items[index],
	), true, nil
}

// GetLoadBalancerName returns a stable load balancer name derived from
// the cluster name, namespace, and service name, truncated to at most 63
// characters.
func (l *LoadBalancer) GetLoadBalancerName(
	ctx context.Context,
	clusterName string,
	service *v1.Service,
) string {
	name := fmt.Sprintf(
		"%s-%s-%s", clusterName, service.Namespace, service.Name,
	)

	if len(name) > 63 {
		name = name[:63]
	}

	name = strings.TrimRight(name, "-")

	return name
}

// EnsureLoadBalancer creates a floating IP if it does not exist, attaches it to
// the first node in nodes order by name, and returns the load balancer status
// with the floating IP address and node's internal IP addresses.
func (l *LoadBalancer) EnsureLoadBalancer(
	ctx context.Context,
	clusterName string,
	service *v1.Service,
	nodes []*v1.Node,
) (*v1.LoadBalancerStatus, error) {
	if service.Spec.ExternalTrafficPolicy != v1.ServiceExternalTrafficPolicyCluster {
		return nil, fmt.Errorf(
			"unsupported external traffic policy %q, only %q is supported",
			service.Spec.ExternalTrafficPolicy,
			v1.ServiceExternalTrafficPolicyCluster,
		)
	}

	if len(nodes) == 0 {
		return nil, errors.New("no nodes for service")
	}

	sortedNodes := slices.Clone(nodes)
	slices.SortStableFunc(sortedNodes, func(a, b *v1.Node) int {
		return strings.Compare(a.Name, b.Name)
	})

	instanceID, err := InstanceIDFromProviderID(sortedNodes[0].Spec.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed fetching instance id from provider id: %w", err)
	}

	floatingIPName := l.GetLoadBalancerName(ctx, clusterName, service)

	allocator, err := addressAllocatorFromAnnotations(
		service.Annotations,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed parsing annotations: %w", err,
		)
	}

	floatingIP, err := l.ensureLoadBalancer(
		ctx, floatingIPName, allocator,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed ensuring floating ip %s: %w",
			floatingIPName, err,
		)
	}

	floatingIP, err = l.attachFloatingIPToInstance(
		ctx, floatingIP, instanceID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed attaching floating ip %s to instance: %w",
			floatingIPName, err,
		)
	}

	return toLoadBalancerStatus(floatingIP, sortedNodes[0]), nil
}

// UpdateLoadBalancer updates an existing load balancer by attaching the
// floating IP to the first node ordered by name.
func (l *LoadBalancer) UpdateLoadBalancer(
	ctx context.Context,
	clusterName string,
	service *v1.Service,
	nodes []*v1.Node,
) error {
	if len(nodes) == 0 {
		return errors.New("no nodes for service")
	}

	sortedNodes := slices.Clone(nodes)
	slices.SortStableFunc(sortedNodes, func(a, b *v1.Node) int {
		return strings.Compare(a.Name, b.Name)
	})

	instanceID, err := InstanceIDFromProviderID(sortedNodes[0].Spec.ProviderID)
	if err != nil {
		return fmt.Errorf(
			"failed fetching instance id from provider id: %w", err,
		)
	}

	floatingIPName := l.GetLoadBalancerName(ctx, clusterName, service)

	floatingIP, err := l.client.FloatingIpView(
		ctx, oxide.FloatingIpViewParams{
			FloatingIp: oxide.NameOrId(floatingIPName),
			Project:    oxide.NameOrId(l.project),
		},
	)
	if err != nil {
		return fmt.Errorf(
			"failed viewing floating ip %s: %w", floatingIPName, err,
		)
	}

	_, err = l.attachFloatingIPToInstance(
		ctx, floatingIP, instanceID,
	)
	return err
}

// EnsureLoadBalancerDeleted detaches and deletes the floating IP.
func (l *LoadBalancer) EnsureLoadBalancerDeleted(
	ctx context.Context,
	clusterName string,
	service *v1.Service,
) error {
	floatingIPName := l.GetLoadBalancerName(ctx, clusterName, service)

	floatingIP, err := l.client.FloatingIpView(
		ctx, oxide.FloatingIpViewParams{
			FloatingIp: oxide.NameOrId(floatingIPName),
			Project:    oxide.NameOrId(l.project),
		},
	)
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") {
			return nil
		}
		return fmt.Errorf(
			"failed viewing floating ip %s: %w", floatingIPName, err,
		)
	}

	if floatingIP.InstanceId != "" {
		_, err = l.client.FloatingIpDetach(
			ctx, oxide.FloatingIpDetachParams{
				FloatingIp: oxide.NameOrId(floatingIP.Id),
			},
		)
		if err != nil {
			return fmt.Errorf(
				"failed detaching floating ip %s: %w",
				floatingIPName, err,
			)
		}
	}

	err = l.client.FloatingIpDelete(
		ctx, oxide.FloatingIpDeleteParams{
			FloatingIp: oxide.NameOrId(floatingIP.Id),
		},
	)
	if err != nil {
		return fmt.Errorf(
			"failed deleting floating ip %s: %w", floatingIPName, err,
		)
	}

	return nil
}

// attachFloatingIPToInstance attaches a floating IP to the given instance. If
// the floating IP is already attached to the instance, this is a no-op. If
// the floating IP is attached to a different instance, it is detached first.
func (l *LoadBalancer) attachFloatingIPToInstance(
	ctx context.Context,
	floatingIP *oxide.FloatingIp,
	instanceID string,
) (*oxide.FloatingIp, error) {
	if floatingIP.InstanceId == instanceID {
		return floatingIP, nil
	}

	if floatingIP.InstanceId != "" {
		_, err := l.client.FloatingIpDetach(
			ctx, oxide.FloatingIpDetachParams{
				FloatingIp: oxide.NameOrId(floatingIP.Id),
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"failed detaching floating ip %s: %w",
				floatingIP.Name, err,
			)
		}
	}

	floatingIP, err := l.client.FloatingIpAttach(
		ctx, oxide.FloatingIpAttachParams{
			FloatingIp: oxide.NameOrId(floatingIP.Id),
			Body: &oxide.FloatingIpAttach{
				Kind:   oxide.FloatingIpParentKindInstance,
				Parent: oxide.NameOrId(instanceID),
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed attaching floating ip to instance %s: %w",
			instanceID, err,
		)
	}

	return floatingIP, nil
}

// ensureLoadBalancer returns the existing floating IP if it matches
// the desired allocator, or deletes and recreates it if the
// configuration has changed. Creates a new one if it does not
// exist.
func (l *LoadBalancer) ensureLoadBalancer(
	ctx context.Context,
	name string,
	allocator oxide.AddressAllocator,
) (*oxide.FloatingIp, error) {
	fip, err := l.client.FloatingIpView(
		ctx, oxide.FloatingIpViewParams{
			FloatingIp: oxide.NameOrId(name),
			Project:    oxide.NameOrId(l.project),
		},
	)
	if err != nil {
		if !strings.Contains(err.Error(), "NotFound") {
			return nil, fmt.Errorf(
				"failed viewing floating ip %s: %w", name, err,
			)
		}
		return l.createFloatingIP(ctx, name, allocator)
	}

	needsRecreate, err := l.floatingIPNeedsRecreate(
		ctx, fip, allocator,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed checking if floating ip %s needs recreate: %w",
			name, err,
		)
	}

	if !needsRecreate {
		return fip, nil
	}

	if fip.InstanceId != "" {
		_, err = l.client.FloatingIpDetach(
			ctx, oxide.FloatingIpDetachParams{
				FloatingIp: oxide.NameOrId(fip.Id),
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"failed detaching floating ip %s: %w",
				name, err,
			)
		}
	}

	err = l.client.FloatingIpDelete(
		ctx, oxide.FloatingIpDeleteParams{
			FloatingIp: oxide.NameOrId(fip.Id),
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed deleting floating ip %s: %w", name, err,
		)
	}

	return l.createFloatingIP(ctx, name, allocator)
}

// createFloatingIP creates a new floating IP with the given name and allocator.
func (l *LoadBalancer) createFloatingIP(
	ctx context.Context,
	name string,
	allocator oxide.AddressAllocator,
) (*oxide.FloatingIp, error) {
	fip, err := l.client.FloatingIpCreate(
		ctx, oxide.FloatingIpCreateParams{
			Project: oxide.NameOrId(l.project),
			Body: &oxide.FloatingIpCreate{
				Name:             oxide.Name(name),
				Description:      "Managed by oxide-cloud-controller-manager.",
				AddressAllocator: allocator,
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed creating floating ip %s: %w", name, err,
		)
	}

	return fip, nil
}

// floatingIPNeedsRecreate compares the existing floating IP
// against the desired allocator configuration and returns true
// if the floating IP needs to be deleted and recreated.
func (l *LoadBalancer) floatingIPNeedsRecreate(
	ctx context.Context,
	fip *oxide.FloatingIp,
	allocator oxide.AddressAllocator,
) (bool, error) {
	if explicit, ok := allocator.AsExplicit(); ok {
		return fip.Ip != explicit.Ip, nil
	}

	auto, ok := allocator.AsAuto()
	if !ok {
		return false, nil
	}

	if ps, ok := auto.PoolSelector.AsExplicit(); ok {
		pool, err := l.client.IpPoolView(
			ctx, oxide.IpPoolViewParams{
				Pool: ps.Pool,
			},
		)
		if err != nil {
			return false, fmt.Errorf(
				"failed viewing ip pool %s: %w",
				ps.Pool, err,
			)
		}
		return fip.IpPoolId != pool.Id, nil
	}

	if ps, ok := auto.PoolSelector.AsAuto(); ok {
		if ps.IpVersion != "" {
			addr, err := netip.ParseAddr(fip.Ip)
			if err != nil {
				return false, fmt.Errorf(
					"failed parsing floating ip address %s: %w",
					fip.Ip, err,
				)
			}
			wantV4 := ps.IpVersion == oxide.IpVersionV4
			return addr.Is4() != wantV4, nil
		}
	}

	return false, nil
}

// addressAllocatorFromAnnotations builds an AddressAllocator from
// the service annotations.
func addressAllocatorFromAnnotations(
	annotations map[string]string,
) (oxide.AddressAllocator, error) {
	ip := annotations[AnnotationFloatingIP]
	pool := annotations[AnnotationFloatingIPPool]
	version := annotations[AnnotationFloatingIPVersion]

	if ip != "" && (pool != "" || version != "") {
		return oxide.AddressAllocator{}, fmt.Errorf(
			"annotation %s is mutually exclusive with %s and %s",
			AnnotationFloatingIP,
			AnnotationFloatingIPPool,
			AnnotationFloatingIPVersion,
		)
	}

	if pool != "" && version != "" {
		return oxide.AddressAllocator{}, fmt.Errorf(
			"annotation %s is mutually exclusive with %s",
			AnnotationFloatingIPPool,
			AnnotationFloatingIPVersion,
		)
	}

	if ip != "" {
		return oxide.AddressAllocator{
			Value: &oxide.AddressAllocatorExplicit{Ip: ip},
		}, nil
	}

	if pool != "" {
		return oxide.AddressAllocator{
			Value: &oxide.AddressAllocatorAuto{
				PoolSelector: oxide.PoolSelector{
					Value: &oxide.PoolSelectorExplicit{
						Pool: oxide.NameOrId(pool),
					},
				},
			},
		}, nil
	}

	if version != "" {
		v := oxide.IpVersion(version)
		if v != oxide.IpVersionV4 && v != oxide.IpVersionV6 {
			return oxide.AddressAllocator{}, fmt.Errorf(
				"invalid %s value %q, must be %q or %q",
				AnnotationFloatingIPVersion,
				version,
				oxide.IpVersionV4,
				oxide.IpVersionV6,
			)
		}
		return oxide.AddressAllocator{
			Value: &oxide.AddressAllocatorAuto{
				PoolSelector: oxide.PoolSelector{
					Value: &oxide.PoolSelectorAuto{
						IpVersion: v,
					},
				},
			},
		}, nil
	}

	return oxide.AddressAllocator{
		Value: &oxide.AddressAllocatorAuto{},
	}, nil
}

// toLoadBalancerStatus builds a LoadBalancerStatus from the floating IP and
// optional node.
func toLoadBalancerStatus(floatingIP *oxide.FloatingIp, node *v1.Node) *v1.LoadBalancerStatus {
	ingress := make([]v1.LoadBalancerIngress, 0)
	if floatingIP != nil {
		ingress = append(ingress, v1.LoadBalancerIngress{
			IP:     floatingIP.Ip,
			IPMode: new(v1.LoadBalancerIPModeProxy),
		})
	}

	if node != nil {
		for _, address := range node.Status.Addresses {
			if address.Type == v1.NodeInternalIP {
				ingress = append(ingress, v1.LoadBalancerIngress{
					IP: address.Address,
				})
			}
		}
	}

	return &v1.LoadBalancerStatus{Ingress: ingress}
}
