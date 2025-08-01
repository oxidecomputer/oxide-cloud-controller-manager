package oxide

import (
	"io"

	cloudprovider "k8s.io/cloud-provider"
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

var _ cloudprovider.Interface = (*CloudProvider)(nil)

// CloudProvider is the Oxide Kubernetes cloud provider. It implements the
// [cloudprovider.Interface] to provide Oxide specific Kubernetes controllers.
type CloudProvider struct{}

func (c *CloudProvider) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
	return
}

// ProviderName returns the name of this cloud provider.
func (c *CloudProvider) ProviderName() string {
	return CloudProviderName
}

// HasClusterID is purposefully unsupported. Oxide does not have resource
// tags which are normally used to uniquely identify resources when multiple
// Kubernetes clusters share the same VPC or load balancers. This may become
// supported in the future when Oxide has resource tags.
func (c *CloudProvider) HasClusterID() bool {
	return false
}

// Clusters is purposefully unsupported. This was meant for a single Cloud
// Controller Manager to manage multiple Kubernetes clusters but the modern
// idiom is to run a single Cloud Controller Manager per Kubernetes cluster,
// making this irrelevant.
func (c *CloudProvider) Clusters() (cloudprovider.Clusters, bool) {
	return nil, false
}

// Instances is purposefully unsupported. Use [CloudProvider.InstancesV2]
// instead.
func (c *CloudProvider) Instances() (cloudprovider.Instances, bool) {
	return nil, false
}

func (c *CloudProvider) InstancesV2() (cloudprovider.InstancesV2, bool) {
	return nil, false
}

func (c *CloudProvider) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

// Routes is purposefully unsupported. It is expected that the Kubernetes
// cluster uses a third-party CNI instead of this controller. This may become
// supported in the future if there's a use case for it.
func (c *CloudProvider) Routes() (cloudprovider.Routes, bool) {
	return nil, false
}

// Zones is purposefully unsupported. Zone and region information is retrieved
// from [CloudProvider.InstancesV2] instead.
func (c *CloudProvider) Zones() (cloudprovider.Zones, bool) {
	return nil, false
}
