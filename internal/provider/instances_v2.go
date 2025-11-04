package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/oxidecomputer/oxide.go/oxide"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	cloudprovider "k8s.io/cloud-provider"
)

var _ cloudprovider.InstancesV2 = (*InstancesV2)(nil)

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
	var (
		err        error
		instance   *oxide.Instance
		instanceID string
	)

	if node.Spec.ProviderID != "" {
		instanceID, err = InstanceIDFromProviderID(node.Spec.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("failed retrieving instance id from provider id: %w", err)
		}

		instance, err = i.client.InstanceView(ctx, oxide.InstanceViewParams{
			Instance: oxide.NameOrId(instanceID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed viewing oxide instance by id: %v", err)
		}
	} else {
		instance, err = i.client.InstanceView(ctx, oxide.InstanceViewParams{
			Project:  oxide.NameOrId(i.project),
			Instance: oxide.NameOrId(node.GetName()),
		})
		if err != nil {
			return nil, fmt.Errorf("failed viewing oxide instance by name: %v", err)
		}

		instanceID = instance.Id
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
		InstanceType:  fmt.Sprintf("%v-%v", instance.Ncpus, (instance.Memory / (1024 * 1024 * 1024))),
		NodeAddresses: nodeAddresses,
	}, nil
}

// InstanceShutdown checks whether the provided node is shut down in Oxide.
func (i *InstancesV2) InstanceShutdown(ctx context.Context, node *v1.Node) (bool, error) {
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
