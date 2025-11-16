// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/oxidecomputer/oxide.go/oxide"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
)

var _ cloudprovider.LoadBalancer = (*LoadBalancer)(nil)

// LoadBalancer implements [cloudprovider.LoadBalancer] to provide Oxide specific
// load balancer functionality using floating IPs.
type LoadBalancer struct {
	client    *oxide.Client
	project   string
	k8sClient kubernetes.Interface
}

// GetLoadBalancerName returns the name of the load balancer for the given service.
// The name follows the format "lb-{namespace}-{service-name}".
func (lb *LoadBalancer) GetLoadBalancerName(ctx context.Context, clusterName string, service *v1.Service) string {
	return fmt.Sprintf("lb-%s-%s", service.Namespace, service.Name)
}

// GetLoadBalancer returns the load balancer status for the given service.
// It checks if a floating IP exists with the expected name and returns its status.
func (lb *LoadBalancer) GetLoadBalancer(ctx context.Context, clusterName string, service *v1.Service) (*v1.LoadBalancerStatus, bool, error) {
	name := lb.GetLoadBalancerName(ctx, clusterName, service)

	klog.V(4).InfoS("getting load balancer", "name", name, "service", service.Name, "namespace", service.Namespace)

	floatingIP, err := lb.client.FloatingIpView(ctx, oxide.FloatingIpViewParams{
		Project:    oxide.NameOrId(lb.project),
		FloatingIp: oxide.NameOrId(name),
	})
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed viewing floating ip %s: %w", name, err)
	}

	status := &v1.LoadBalancerStatus{
		Ingress: []v1.LoadBalancerIngress{
			{
				IP: floatingIP.Ip,
			},
		},
	}

	return status, true, nil
}

// EnsureLoadBalancer creates or updates a load balancer for the given service.
// It creates a floating IP and attaches it to a control plane node.
func (lb *LoadBalancer) EnsureLoadBalancer(ctx context.Context, clusterName string, service *v1.Service, nodes []*v1.Node) (*v1.LoadBalancerStatus, error) {
	name := lb.GetLoadBalancerName(ctx, clusterName, service)

	klog.InfoS("ensuring load balancer", "name", name, "service", service.Name, "namespace", service.Namespace)

	// Find a control plane node
	controlPlaneNode, err := lb.findControlPlaneNode(ctx, nodes)
	if err != nil {
		return nil, fmt.Errorf("failed finding control plane node: %w", err)
	}

	instanceID, err := InstanceIDFromProviderID(controlPlaneNode.Spec.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving instance id from provider id: %w", err)
	}

	// Check if floating IP already exists
	floatingIP, err := lb.client.FloatingIpView(ctx, oxide.FloatingIpViewParams{
		Project:    oxide.NameOrId(lb.project),
		FloatingIp: oxide.NameOrId(name),
	})
	if err != nil && !strings.Contains(err.Error(), "NotFound") {
		return nil, fmt.Errorf("failed viewing floating ip %s: %w", name, err)
	}

	// Create floating IP if it doesn't exist
	if floatingIP == nil {
		klog.V(2).InfoS("creating floating ip", "name", name)

		floatingIP, err = lb.client.FloatingIpCreate(ctx, oxide.FloatingIpCreateParams{
			Project: oxide.NameOrId(lb.project),
			Body: &oxide.FloatingIpCreate{
				Description: fmt.Sprintf("Load balancer for service %s/%s", service.Namespace, service.Name),
				Name:        oxide.Name(name),
				Pool:        oxide.NameOrId("default"),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed creating floating ip %s: %w", name, err)
		}

		klog.InfoS("created floating ip", "name", name, "ip", floatingIP.Ip)
	}

	// Attach floating IP to the control plane node if not already attached
	if floatingIP.InstanceId == "" || floatingIP.InstanceId != instanceID {
		// If it's attached to a different instance, detach it first
		if floatingIP.InstanceId != "" {
			klog.V(2).InfoS("detaching floating ip from previous instance", "name", name, "instance", floatingIP.InstanceId)

			if _, err := lb.client.FloatingIpDetach(ctx, oxide.FloatingIpDetachParams{
				Project:    oxide.NameOrId(lb.project),
				FloatingIp: oxide.NameOrId(name),
			}); err != nil {
				return nil, fmt.Errorf("failed detaching floating ip %s from instance %s: %w", name, floatingIP.InstanceId, err)
			}
		}

		klog.V(2).InfoS("attaching floating ip to control plane node", "name", name, "instance", instanceID, "node", controlPlaneNode.Name)

		floatingIP, err = lb.client.FloatingIpAttach(ctx, oxide.FloatingIpAttachParams{
			Project:    oxide.NameOrId(lb.project),
			FloatingIp: oxide.NameOrId(name),
			Body: &oxide.FloatingIpAttach{
				Kind:   oxide.FloatingIpParentKindInstance,
				Parent: oxide.NameOrId(instanceID),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed attaching floating ip %s to instance %s: %w", name, instanceID, err)
		}

		klog.InfoS("attached floating ip to control plane node", "name", name, "ip", floatingIP.Ip, "node", controlPlaneNode.Name)
	}

	status := &v1.LoadBalancerStatus{
		Ingress: []v1.LoadBalancerIngress{
			{
				IP: floatingIP.Ip,
			},
		},
	}

	return status, nil
}

// UpdateLoadBalancer updates the hosts under the specified load balancer.
// It ensures the floating IP is attached to an available control plane node.
func (lb *LoadBalancer) UpdateLoadBalancer(ctx context.Context, clusterName string, service *v1.Service, nodes []*v1.Node) error {
	name := lb.GetLoadBalancerName(ctx, clusterName, service)

	klog.InfoS("updating load balancer", "name", name, "service", service.Name, "namespace", service.Namespace)

	// Find a control plane node
	controlPlaneNode, err := lb.findControlPlaneNode(ctx, nodes)
	if err != nil {
		return fmt.Errorf("failed finding control plane node: %w", err)
	}

	instanceID, err := InstanceIDFromProviderID(controlPlaneNode.Spec.ProviderID)
	if err != nil {
		return fmt.Errorf("failed retrieving instance id from provider id: %w", err)
	}

	// Get the floating IP
	floatingIP, err := lb.client.FloatingIpView(ctx, oxide.FloatingIpViewParams{
		Project:    oxide.NameOrId(lb.project),
		FloatingIp: oxide.NameOrId(name),
	})
	if err != nil {
		return fmt.Errorf("failed viewing floating ip %s: %w", name, err)
	}

	// Update attachment if necessary
	if floatingIP.InstanceId == "" || floatingIP.InstanceId != instanceID {
		// Detach from current instance if attached
		if floatingIP.InstanceId != "" {
			klog.V(2).InfoS("detaching floating ip from previous instance", "name", name, "instance", floatingIP.InstanceId)

			if _, err := lb.client.FloatingIpDetach(ctx, oxide.FloatingIpDetachParams{
				Project:    oxide.NameOrId(lb.project),
				FloatingIp: oxide.NameOrId(name),
			}); err != nil {
				return fmt.Errorf("failed detaching floating ip %s: %w", name, err)
			}
		}

		klog.V(2).InfoS("attaching floating ip to control plane node", "name", name, "instance", instanceID, "node", controlPlaneNode.Name)

		if _, err := lb.client.FloatingIpAttach(ctx, oxide.FloatingIpAttachParams{
			Project:    oxide.NameOrId(lb.project),
			FloatingIp: oxide.NameOrId(name),
			Body: &oxide.FloatingIpAttach{
				Kind:   oxide.FloatingIpParentKindInstance,
				Parent: oxide.NameOrId(instanceID),
			},
		}); err != nil {
			return fmt.Errorf("failed attaching floating ip %s to instance %s: %w", name, instanceID, err)
		}

		klog.InfoS("updated floating ip attachment", "name", name, "node", controlPlaneNode.Name)
	}

	return nil
}

// EnsureLoadBalancerDeleted deletes the specified load balancer.
// It detaches and deletes the floating IP associated with the service.
func (lb *LoadBalancer) EnsureLoadBalancerDeleted(ctx context.Context, clusterName string, service *v1.Service) error {
	name := lb.GetLoadBalancerName(ctx, clusterName, service)

	klog.InfoS("ensuring load balancer deleted", "name", name, "service", service.Name, "namespace", service.Namespace)

	// Get the floating IP to check if it's attached
	floatingIP, err := lb.client.FloatingIpView(ctx, oxide.FloatingIpViewParams{
		Project:    oxide.NameOrId(lb.project),
		FloatingIp: oxide.NameOrId(name),
	})
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") {
			klog.V(2).InfoS("floating ip not found, already deleted", "name", name)
			return nil
		}
		return fmt.Errorf("failed viewing floating ip %s: %w", name, err)
	}

	// Detach the floating IP if it's attached to an instance
	if floatingIP.InstanceId != "" {
		klog.V(2).InfoS("detaching floating ip", "name", name, "instance", floatingIP.InstanceId)

		if _, err := lb.client.FloatingIpDetach(ctx, oxide.FloatingIpDetachParams{
			Project:    oxide.NameOrId(lb.project),
			FloatingIp: oxide.NameOrId(name),
		}); err != nil {
			return fmt.Errorf("failed detaching floating ip %s: %w", name, err)
		}
	}

	// Delete the floating IP
	klog.V(2).InfoS("deleting floating ip", "name", name)

	if err := lb.client.FloatingIpDelete(ctx, oxide.FloatingIpDeleteParams{
		Project:    oxide.NameOrId(lb.project),
		FloatingIp: oxide.NameOrId(name),
	}); err != nil {
		return fmt.Errorf("failed deleting floating ip %s: %w", name, err)
	}

	klog.InfoS("deleted floating ip", "name", name)

	return nil
}

// findControlPlaneNode finds the first available control plane node from the provided list.
// Control plane nodes are identified by the presence of the "node-role.kubernetes.io/control-plane"
// or "node-role.kubernetes.io/master" label.
func (lb *LoadBalancer) findControlPlaneNode(ctx context.Context, nodes []*v1.Node) (*v1.Node, error) {
	for _, node := range nodes {
		if node.Labels == nil {
			continue
		}

		// Check for control plane label (current standard)
		if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
			klog.V(4).InfoS("found control plane node", "node", node.Name)
			return node, nil
		}

		// Check for master label (legacy, but still supported)
		if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
			klog.V(4).InfoS("found master node", "node", node.Name)
			return node, nil
		}
	}

	return nil, fmt.Errorf("no control plane node found among %d nodes", len(nodes))
}
