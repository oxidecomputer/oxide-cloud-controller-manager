package oxide

import (
	"fmt"
	"io"

	oxide "github.com/oxidecomputer/oxide.go"
	cloudprovider "k8s.io/cloud-provider"
)

// ProviderName is the name of this cloud provider.
const ProviderName = "oxide"

type cloud struct {
	client *oxide.Client
}

func newCloud() (cloudprovider.Interface, error) {
	// Create a new client with your token and host parsed from the environment
	// variables: OXIDE_TOKEN, OXIDE_HOST.
	client, err := oxide.NewClientFromEnv("k8s.io/cloud-provider-oxide")
	if err != nil {
		return nil, fmt.Errorf("unable to create oxide api client: %v", err)
	}

	return &cloud{
		client: client,
	}, nil
}

func init() {
	cloudprovider.RegisterCloudProvider(ProviderName, func(io.Reader) (cloudprovider.Interface, error) {
		return newCloud()
	})
}

// Initialize provides the cloud with a kubernetes client builder and may spawn goroutines
// to perform housekeeping or run custom controllers specific to the cloud provider.
// Any tasks started here should be cleaned up when the stop channel closes.
func (c *cloud) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
}

// LoadBalancer returns a balancer interface. Also returns true if the interface is supported, false otherwise.
// TODO: implement this interface when we have a load balancers API.
func (c *cloud) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

// Instances returns an instances interface. Also returns true if the interface is supported, false otherwise.
func (c *cloud) Instances() (cloudprovider.Instances, bool) {
	return nil, false
}

// InstancesV2 is an implementation for instances and should only be implemented by external cloud providers.
// Implementing InstancesV2 is behaviorally identical to Instances but is optimized to significantly reduce
// API calls to the cloud provider when registering and syncing nodes. Implementation of this interface will
// disable calls to the Zones interface. Also returns true if the interface is supported, false otherwise.
func (c *cloud) InstancesV2() (cloudprovider.InstancesV2, bool) {
	return newInstances(), true
}

// Zones returns a zones interface. Also returns true if the interface is supported, false otherwise.
// DEPRECATED: Zones is deprecated in favor of retrieving zone/region information from InstancesV2.
// This interface will not be called if InstancesV2 is enabled.
func (c *cloud) Zones() (cloudprovider.Zones, bool) {
	return nil, false
}

// Clusters returns a clusters interface.  Also returns true if the interface is supported, false otherwise.
func (c *cloud) Clusters() (cloudprovider.Clusters, bool) {
	return nil, false
}

// Routes returns a routes interface along with whether the interface is supported.
func (c *cloud) Routes() (cloudprovider.Routes, bool) {
	return newRoutes(), true
}

// ProviderName returns the cloud provider ID.
func (c *cloud) ProviderName() string {
	return ProviderName
}

// HasClusterID returns true if a ClusterID is required and set.
func (c *cloud) HasClusterID() bool {
	return false
}
