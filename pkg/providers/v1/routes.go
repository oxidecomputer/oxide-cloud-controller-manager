package oxide

import (
	"context"

	cloudprovider "k8s.io/cloud-provider"
)

type routes struct {
}

func newRoutes() cloudprovider.Routes {
	return &routes{}
}

// Routes is an abstract, pluggable interface for advanced routing rules.
func (r *routes) ListRoutes(ctx context.Context, clusterName string) ([]*cloudprovider.Route, error) {
	return nil, nil
}

// CreateRoute creates the described managed route
// route.Name will be ignored, although the cloud-provider may use nameHint
// to create a more user-meaningful name.
func (r *routes) CreateRoute(ctx context.Context, clusterName string, nameHint string, route *cloudprovider.Route) error {
	return nil
}

// DeleteRoute deletes the specified managed route
// Route should be as returned by ListRoutes
func (r *routes) DeleteRoute(ctx context.Context, clusterName string, route *cloudprovider.Route) error {
	return nil
}
