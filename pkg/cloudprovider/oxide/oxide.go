package oxide

import (
	"io"

	cloudprovider "k8s.io/cloud-provider"
)

func init() {
	cloudprovider.RegisterCloudProvider(CloudProviderName, func(config io.Reader) (cloudprovider.Interface, error) {
		return &CloudProvider{}, nil
	})
}

const CloudProviderName = "oxide"

var _ cloudprovider.Interface = (*CloudProvider)(nil)

type CloudProvider struct{}

func (c *CloudProvider) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
	return
}

func (c *CloudProvider) ProviderName() string {
	return CloudProviderName
}

func (c *CloudProvider) HasClusterID() bool {
	return false
}

func (c *CloudProvider) Clusters() (cloudprovider.Clusters, bool) {
	return nil, false
}

func (c *CloudProvider) Instances() (cloudprovider.Instances, bool) {
	return nil, false
}

func (c *CloudProvider) InstancesV2() (cloudprovider.InstancesV2, bool) {
	return nil, false
}

func (c *CloudProvider) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

func (c *CloudProvider) Routes() (cloudprovider.Routes, bool) {
	return nil, false
}

func (c *CloudProvider) Zones() (cloudprovider.Zones, bool) {
	return nil, false
}
