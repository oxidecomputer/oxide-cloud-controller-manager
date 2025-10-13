package oxide

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/oxidecomputer/oxide.go/oxide"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
)

func init() {
	cloudprovider.RegisterCloudProvider(
		CloudProviderName,
		func(config io.Reader) (cloudprovider.Interface, error) {
			return &CloudProvider{}, nil
		},
	)
}

// CloudProviderName is the name of this cloud provider.
const CloudProviderName = "oxide"

const (
	oxideProjectEnv = "OXIDE_PROJECT"
)

var _ cloudprovider.Interface = (*CloudProvider)(nil)

// CloudProvider is the Oxide Kubernetes cloud provider. It implements
// [cloudprovider.Interface] to provide Oxide specific Kubernetes controllers.
type CloudProvider struct {
	oxideClient      *oxide.Client
	kubernetesClient kubernetes.Interface
}

// Initialize creates the Kubernetes and Oxide clients and spawns any additional
// controllers, if necessary.
func (c *CloudProvider) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
	kubernetesClient, err := clientBuilder.Client(CloudProviderName)
	if err != nil {
		klog.Fatalf("failed to initialize kubernetes client: %v", err)
		return
	}

	c.kubernetesClient = kubernetesClient
	klog.InfoS("initialized client", "type", "kubernetes")

	oxideClient, err := oxide.NewClient(nil)
	if err != nil {
		klog.Fatalf("failed to create oxide client: %v", err)
		return
	}

	c.oxideClient = oxideClient
	klog.InfoS("initialized client", "type", "oxide")
}

// ProviderName returns the name of this cloud provider.
func (c *CloudProvider) ProviderName() string {
	return CloudProviderName
}

// HasClusterID is purposefully unimplemented. A cluster ID is used to uniquely
// identify resources for a specific Kubernetes cluster when multiple Kubernetes
// clusters can conflict with each other when using shared resources (e.g.,
// VPC, load balancer). Usually, such resources are tagged or labeled with the
// cluster ID but Oxide does not have resource tags or labels. Additionally,
// it's expected that a Kubernetes cluster on Oxide is deployed in its own VPC
// and does not share resources with other Kubernetes clusters. This may become
// supported in the future when Oxide has resource tags or labels.
func (c *CloudProvider) HasClusterID() bool {
	return false
}

// Clusters is purposefully unimplemented. This is meant for a single Cloud
// Controller Manager to manage multiple Kubernetes clusters but the modern
// idiom is to run a single Cloud Controller Manager per Kubernetes cluster,
// making this irrelevant.
func (c *CloudProvider) Clusters() (cloudprovider.Clusters, bool) {
	return nil, false
}

// Instances is purposefully unimplemented. Use [CloudProvider.InstancesV2].
func (c *CloudProvider) Instances() (cloudprovider.Instances, bool) {
	return nil, false
}

// InstancesV2 returns an implementation of [cloudprovider.InstancesV2]
// that's used to provide the node controller and node lifecycle controller to
// initialize Kubernetes nodes, provide their metadata, and determine whether
// the nodes exists to facilitate node cleanup in Kubernetes.
func (c *CloudProvider) InstancesV2() (cloudprovider.InstancesV2, bool) {
	return &CloudProviderInstancesV2{
		oxideClient:      c.oxideClient,
		kubernetesClient: c.kubernetesClient,
	}, true
}

// LoadBalancer is currently unimplemented but will soon be.
func (c *CloudProvider) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

// Routes is purposefully unimplemented. It is expected that the Kubernetes
// cluster uses a third-party CNI instead of this controller. This may become
// supported in the future if there's a use case for it.
func (c *CloudProvider) Routes() (cloudprovider.Routes, bool) {
	return nil, false
}

// Zones is purposefully unimplemented. Zone and region information is retrieved
// from [CloudProviderInstancesV2.InstanceMetadata] instead.
func (c *CloudProvider) Zones() (cloudprovider.Zones, bool) {
	return nil, false
}

var _ cloudprovider.InstancesV2 = (*CloudProviderInstancesV2)(nil)

// CloudProviderInstancesV2 implements [cloudprovider.InstancesV2] to provide an
// Oxide specific node controller and node lifecycle controller.
type CloudProviderInstancesV2 struct {
	oxideClient      *oxide.Client
	kubernetesClient kubernetes.Interface
}

// InstanceExists checks whether the provided node exists in Oxide.
func (c *CloudProviderInstancesV2) InstanceExists(ctx context.Context, node *v1.Node) (bool, error) {
	instanceID := strings.TrimPrefix(node.Spec.ProviderID, "oxide://")

	if _, err := c.oxideClient.InstanceView(ctx, oxide.InstanceViewParams{
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
func (c *CloudProviderInstancesV2) InstanceMetadata(ctx context.Context, node *v1.Node) (*cloudprovider.InstanceMetadata, error) {
	var (
		err        error
		instance   *oxide.Instance
		instanceID string
	)

	if node.Spec.ProviderID != "" {
		instanceID = strings.TrimPrefix(node.Spec.ProviderID, "oxide://")

		instance, err = c.oxideClient.InstanceView(ctx, oxide.InstanceViewParams{
			Instance: oxide.NameOrId(instanceID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed viewing oxide instance by id: %v", err)
		}
	} else {
		instance, err = c.oxideClient.InstanceView(ctx, oxide.InstanceViewParams{
			Project:  oxide.NameOrId(os.Getenv(oxideProjectEnv)),
			Instance: oxide.NameOrId(node.GetName()),
		})
		if err != nil {
			return nil, fmt.Errorf("failed viewing oxide instance by name: %v", err)
		}

		instanceID = instance.Id
	}

	nics, err := c.oxideClient.InstanceNetworkInterfaceList(ctx, oxide.InstanceNetworkInterfaceListParams{
		Instance: oxide.NameOrId(instanceID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed listing instance network interfaces: %v", err)
	}

	externalIPs, err := c.oxideClient.InstanceExternalIpList(ctx, oxide.InstanceExternalIpListParams{
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
		ProviderID:       fmt.Sprintf("oxide://%s", instanceID),
		InstanceType:     fmt.Sprintf("%v-%v", instance.Ncpus, (instance.Memory / (1024 * 1024 * 1024))),
		NodeAddresses:    nodeAddresses,
	}, nil
}

// InstanceShutdown checks whether the provided node is shut down in Oxide.
func (c *CloudProviderInstancesV2) InstanceShutdown(ctx context.Context, node *v1.Node) (bool, error) {
	instanceID := strings.TrimPrefix(node.Spec.ProviderID, "oxide://")

	instance, err := c.oxideClient.InstanceView(ctx, oxide.InstanceViewParams{
		Instance: oxide.NameOrId(instanceID),
	})
	if err != nil {
		return false, fmt.Errorf("failed viewing oxide instance %s: %v", instanceID, err)
	}

	return instance.RunState == oxide.InstanceStateStopped, nil
}
