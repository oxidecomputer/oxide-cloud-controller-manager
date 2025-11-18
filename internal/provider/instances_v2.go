// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.
package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oxidecomputer/oxide.go/oxide"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	cloudprovider "k8s.io/cloud-provider"
)

var _ cloudprovider.InstancesV2 = (*InstancesV2)(nil)

// gibibyte is the number of bytes in a gibibyte.
const gibibyte = 1024 * 1024 * 1024

// InstancesV2 implements [cloudprovider.InstancesV2] to provide Oxide specific
// instance functionality.
type InstancesV2 struct {
	client  *oxide.Client
	project string

	k8sClient kubernetes.Interface
}

// InstanceExists checks whether the provided Kubernetes node exists as instance
// in Oxide.
func (i *InstancesV2) InstanceExists(ctx context.Context, node *v1.Node) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	instanceID, err := InstanceIDFromProviderID(node.Spec.ProviderID)
	if err != nil {
		return false, fmt.Errorf("failed retrieving instance id from provider id: %w", err)
	}

	if _, err := i.client.InstanceView(ctx, oxide.InstanceViewParams{
		Instance: oxide.NameOrId(instanceID),
	}); err != nil {
		if strings.Contains(err.Error(), "NotFound") {
			return false, nil
		}

		return false, fmt.Errorf("failed viewing oxide instance %s: %v", instanceID, err)
	}

	return true, nil
}

// InstanceMetadata populates the metadata for the provided node, notably
// setting its provider ID.
func (i *InstancesV2) InstanceMetadata(ctx context.Context, node *v1.Node) (*cloudprovider.InstanceMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Get the instance ID, either from the provider ID or by looking up by name.
	instanceID, err := i.getInstanceID(ctx, node)
	if err != nil {
		return nil, err
	}

	// Retrieve the instance details.
	instance, err := i.client.InstanceView(ctx, oxide.InstanceViewParams{
		Instance: oxide.NameOrId(instanceID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed viewing oxide instance: %v", err)
	}

	nics, err := i.client.InstanceNetworkInterfaceList(ctx, oxide.InstanceNetworkInterfaceListParams{
		Instance: oxide.NameOrId(instanceID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed listing instance network interfaces: %v", err)
	}

	externalIPs, err := i.client.InstanceExternalIpList(ctx, oxide.InstanceExternalIpListParams{
		Instance: oxide.NameOrId(instanceID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed listing instance external ips: %v", err)
	}

	nodeAddresses := make([]v1.NodeAddress, 0)
	nodeAddresses = append(nodeAddresses, v1.NodeAddress{
		Type:    v1.NodeHostName,
		Address: instance.Hostname,
	})

	for _, nic := range nics.Items {
		nodeAddresses = append(nodeAddresses, v1.NodeAddress{
			Type:    v1.NodeInternalIP,
			Address: nic.Ip,
		})
	}

	for _, externalIP := range externalIPs.Items {
		if externalIP.Kind == "snat" {
			continue
		}

		nodeAddresses = append(nodeAddresses, v1.NodeAddress{
			Type:    v1.NodeExternalIP,
			Address: externalIP.Ip,
		})
	}

	return &cloudprovider.InstanceMetadata{
		ProviderID:    NewProviderID(instanceID),
		InstanceType:  fmt.Sprintf("%d-%d", instance.Ncpus, instance.Memory/gibibyte),
		NodeAddresses: nodeAddresses,
	}, nil
}

// getInstanceID retrieves the instance ID either from the node's provider ID
// or by looking up the instance by name.
func (i *InstancesV2) getInstanceID(ctx context.Context, node *v1.Node) (string, error) {
	if node.Spec.ProviderID != "" {
		return InstanceIDFromProviderID(node.Spec.ProviderID)
	}

	// If no provider ID is set, look up the instance by name.
	instance, err := i.client.InstanceView(ctx, oxide.InstanceViewParams{
		Project:  oxide.NameOrId(i.project),
		Instance: oxide.NameOrId(node.GetName()),
	})
	if err != nil {
		return "", fmt.Errorf("failed viewing oxide instance by name: %v", err)
	}

	return instance.Id, nil
}

// InstanceShutdown checks whether the provided node is shut down in Oxide.
func (i *InstancesV2) InstanceShutdown(ctx context.Context, node *v1.Node) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	instanceID, err := InstanceIDFromProviderID(node.Spec.ProviderID)
	if err != nil {
		return false, fmt.Errorf("failed retrieving instance id from provider id: %w", err)
	}

	instance, err := i.client.InstanceView(ctx, oxide.InstanceViewParams{
		Instance: oxide.NameOrId(instanceID),
	})
	if err != nil {
		return false, fmt.Errorf("failed viewing oxide instance %s: %v", instanceID, err)
	}

	return instance.RunState == oxide.InstanceStateStopped, nil
}
