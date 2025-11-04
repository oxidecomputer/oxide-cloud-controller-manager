package provider

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/oxidecomputer/oxide.go/oxide"
	"k8s.io/client-go/kubernetes"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
)

// init registers the Oxide cloud provider as a valid external cloud provider
// for Kubernetes.
func init() {
	cloudprovider.RegisterCloudProvider(
		Name,
		func(config io.Reader) (cloudprovider.Interface, error) {
			return &Oxide{}, nil
		},
	)
}

// Name is the name of this cloud provider.
const Name = "oxide"

var _ cloudprovider.Interface = (*Oxide)(nil)

// Oxide is the Oxide cloud provider. It implements [cloudprovider.Interface] to
// provide Oxide specific functionality.
type Oxide struct {
	client  *oxide.Client
	project string

	k8sClient kubernetes.Interface
}

// Initialize creates the Oxide and Kubernetes clients and spawns any additional
// controllers, if necessary.
func (o *Oxide) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
	kubernetesClient, err := clientBuilder.Client(Name)
	if err != nil {
		klog.Fatalf("failed to create kubernetes client: %v", err)
		return
	}
	o.k8sClient = kubernetesClient

	oxideClient, err := oxide.NewClient(nil)
	if err != nil {
		klog.Fatalf("failed to create oxide client: %v", err)
		return
	}
	o.client = oxideClient

	o.project = os.Getenv("OXIDE_PROJECT")

	klog.InfoS("initialized cloud provider", "type", "oxide")
}

// ProviderName returns the name of this cloud provider.
func (o *Oxide) ProviderName() string {
	return Name
}

// HasClusterID is purposefully unimplemented. A cluster ID is used to uniquely
// identify resources for a specific Kubernetes cluster when multiple Kubernetes
// clusters can conflict with each other when using shared resources (e.g.,
// VPC, load balancer). Usually, such resources are tagged or labeled with the
// cluster ID but Oxide does not have resource tags or labels. Additionally,
// it's expected that a Kubernetes cluster on Oxide is deployed in its own VPC
// and does not share resources with other Kubernetes clusters. This may become
// supported in the future when Oxide has resource tags or labels.
func (o *Oxide) HasClusterID() bool {
	return false
}

// Clusters is purposefully unimplemented. This is meant for a single Cloud
// Controller Manager to manage multiple Kubernetes clusters but the modern
// idiom is to run a single Cloud Controller Manager per Kubernetes cluster,
// making this irrelevant.
func (o *Oxide) Clusters() (cloudprovider.Clusters, bool) {
	return nil, false
}

// Instances is purposefully unimplemented. Use [Oxide.InstancesV2].
func (o *Oxide) Instances() (cloudprovider.Instances, bool) {
	return nil, false
}

// InstancesV2 returns an implementation of [cloudprovider.InstancesV2]
// that provides functionality to initialize Kubernetes nodes, provide their
// metadata, and determine whether they exists to facilitate cleanup.
func (o *Oxide) InstancesV2() (cloudprovider.InstancesV2, bool) {
	return &InstancesV2{
		client:    o.client,
		project:   o.project,
		k8sClient: o.k8sClient,
	}, true
}

// LoadBalancer is currently unimplemented. This may be implemented in the
// future.
func (o *Oxide) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

// Routes is purposefully unimplemented. It is expected that the Kubernetes
// cluster uses a third-party CNI instead of this controller. This may be
// implemented in the future.
func (o *Oxide) Routes() (cloudprovider.Routes, bool) {
	return nil, false
}

// Zones is purposefully unimplemented. Zone and region information is retrieved
// from [InstancesV2.InstanceMetadata] instead.
func (o *Oxide) Zones() (cloudprovider.Zones, bool) {
	return nil, false
}

// InstanceIDFromProviderID extracts the Oxide instance ID from a provider ID.
func InstanceIDFromProviderID(providerID string) (string, error) {
	if providerID == "" {
		return "", errors.New("provider id is empty")
	}

	if !strings.HasPrefix(providerID, "oxide://") {
		return "", errors.New("provider id does not have 'oxide://' prefix")
	}

	instanceID := strings.TrimPrefix(providerID, "oxide://")

	if _, err := uuid.Parse(instanceID); err != nil {
		return "", fmt.Errorf("provider id contains invalid uuid: %w", err)
	}

	return instanceID, nil
}

// NewProviderID formats an Oxide instance ID as a provider ID.
func NewProviderID(instanceID string) string {
	return fmt.Sprintf("oxide://%s", instanceID)
}
