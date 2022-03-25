package oxide

import (
	"context"
	"fmt"
	"strings"

	oxide "github.com/oxidecomputer/oxide.go"
	v1 "k8s.io/api/core/v1"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
)

type instances struct {
	cloud *oxideCloud
}

func newInstancesV2(cloud *oxideCloud) cloudprovider.InstancesV2 {
	return &instances{cloud: cloud}
}

// InstanceExists returns true if the instance for the given node exists according to the cloud provider.
// Use the node.name or node.spec.providerID field to find the node in the cloud provider.
func (i *instances) InstanceExists(ctx context.Context, node *v1.Node) (bool, error) {
	_, err := i.getInstance(ctx, node)

	if err != nil {
		klog.V(6).Infof("instance not found for node: %s, %v", node.Name, err)
		return false, cloudprovider.InstanceNotFound
	}

	return true, nil
}

// InstanceShutdown returns true if the instance is shutdown according to the cloud provider.
// Use the node.name or node.spec.providerID field to find the node in the cloud provider.
func (i *instances) InstanceShutdown(ctx context.Context, node *v1.Node) (bool, error) {
	instance, err := i.getInstance(ctx, node)

	if err != nil {
		klog.V(6).Infof("instance not found for node: %s, %v", node.Name, err)
		return false, cloudprovider.InstanceNotFound
	}

	return instance.RunState == oxide.InstanceStateStopped, nil
}

// InstanceMetadata returns the instance's metadata. The values returned in InstanceMetadata are
// translated into specific fields and labels in the Node object on registration.
// Implementations should always check node.spec.providerID first when trying to discover the instance
// for a given node. In cases where node.spec.providerID is empty, implementations can use other
// properties of the node like its name, labels and annotations.
func (i *instances) InstanceMetadata(ctx context.Context, node *v1.Node) (*cloudprovider.InstanceMetadata, error) {
	instance, err := i.getInstance(ctx, node)

	if err != nil {
		klog.V(6).Infof("instance not found for node: %s, %v", node.Name, err)
		return nil, cloudprovider.InstanceNotFound
	}

	metadata := &cloudprovider.InstanceMetadata{
		// ProviderID is a unique ID used to identify an instance on the cloud provider.
		// The ProviderID set here will be set on the node's spec.providerID field.
		// The provider ID format can be set by the cloud provider but providers should
		// ensure the format does not change in any incompatible way.
		//
		// The provider ID format used by existing cloud provider has been:
		//    <provider-name>://<instance-id>
		// Existing providers setting this field should preserve the existing format
		// currently being set in node.spec.providerID.
		ProviderID: getInstanceProviderID(instance),

		// InstanceType is the instance's type.
		// The InstanceType set here will be set using the following labels on the node object:
		//   * node.kubernetes.io/instance-type=<instance-type>
		//   * beta.kubernetes.io/instance-type=<instance-type> (DEPRECATED)
		InstanceType: fmt.Sprintf("ncpus=%d,memory=%d", instance.NCPUs, instance.Memory),

		// NodeAddress contains information for the instance's address.
		// The node addresses returned here will be set on the node's status.addresses field.
		// TODO: Fill in once we know the IPs.
		NodeAddresses: []v1.NodeAddress{},

		// Zone is the zone that the instance is in.
		// The value set here is applied as the following labels on the node:
		//   * topology.kubernetes.io/zone=<zone>
		//   * failure-domain.beta.kubernetes.io/zone=<zone> (DEPRECATED)
		// TODO: We have no concept of zones yet.
		Zone: "",

		// Region is the region that the instance is in.
		// The value set here is applied as the following labels on the node:
		//   * topology.kubernetes.io/region=<region>
		//   * failure-domain.beta.kubernetes.io/region=<region> (DEPRECATED)
		// TODO: We have no concept of regions yet.
		Region: "",
	}

	return metadata, nil
}

// getInstanceProviderID returns the provider ID of an instance which is ultimately set in the node.Spec.ProviderID field.
// The well-known format for a node's providerID is:
//    * oxide://<instance-id>
func getInstanceProviderID(instance *oxide.Instance) string {
	return fmt.Sprintf("%s://%s", ProviderName, instance.ID)
}

// parseInstanceIDFromProviderID parses the node's instance ID based on the following formats:
//   * oxide://<instance-id>
//   * <instance-id>
// This function always assumes a valid providerID format was provided.
func parseInstanceIDFromProviderID(providerID string) (string, error) {
	// trim the provider name prefix 'oxide://', renaming providerID should contain metadata in one of the following formats:
	// * <instance-id>
	instanceID := ""
	metadata := strings.Split(strings.TrimPrefix(providerID, fmt.Sprintf("%s://", ProviderName)), "/")
	if len(metadata) == 1 {
		// instance-id
		instanceID = metadata[0]
	}

	return instanceID, nil
}

// getInstance returns the instance if the instance with the given node info still exists.
// If false an error will be returned, the instance will be immediately deleted by the cloud controller manager.
func (i *instances) getInstance(ctx context.Context, node *v1.Node) (*oxide.Instance, error) {
	if node.Spec.ProviderID == "" {
		// Get the instance by name.
		klog.V(4).Infof("looking for node by name %v", node.Name)
		return i.cloud.client.Instances.Get(oxide.Name(node.Name), oxide.Name(i.cloud.organization), oxide.Name(i.cloud.project))
	}

	// Get the instance by provider ID.
	klog.V(4).Infof("looking for node by provider ID %v", node.Spec.ProviderID)
	instanceID, err := parseInstanceIDFromProviderID(node.Spec.ProviderID)
	if err != nil {
		return nil, err
	}

	return i.cloud.client.Instances.Get(oxide.Name(instanceID), oxide.Name(i.cloud.organization), oxide.Name(i.cloud.project))
}
