// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package provider

import (
	"context"
	"testing"

	"github.com/oxidecomputer/oxide.go/oxide"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type mockOxideClient struct {
	InstanceNetworkInterfaceListOutput *oxide.InstanceNetworkInterfaceResultsPage
	InstanceNetworkInterfaceListError  error

	InstanceExternalIpListOutput *oxide.ExternalIpResultsPage
	InstanceExternalIpListError  error

	InstanceViewOutput *oxide.Instance
	InstanceViewError  error
}

var (
	nodeWithoutProviderID = v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec:       v1.NodeSpec{},
	}
	nodeWithProviderID = v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec: v1.NodeSpec{
			ProviderID: "oxide://12345678-1234-1234-1234-123456789abc",
		},
	}
	nodeDoesNotExistInOxide = v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
		Spec: v1.NodeSpec{
			ProviderID: "oxide://87654321-1234-1234-1234-123456789abc",
		},
	}

	instanceRunning = oxide.Instance{
		Name:     oxide.Name("node-1"),
		Id:       "12345678-1234-1234-1234-123456789abc",
		RunState: oxide.InstanceStateRunning,
	}

	instanceStopped = oxide.Instance{
		Name:     oxide.Name("node-1"),
		Id:       "12345678-1234-1234-1234-123456789abc",
		RunState: oxide.InstanceStateStopped,
	}
)

func TestInstanceExists(t *testing.T) {
	t.Run("WithProviderID", func(t *testing.T) {
		instancesV2 := InstancesV2{
			client: &mockOxideClient{
				InstanceViewOutput: &instanceRunning,
			},
			project: "test",
		}
		exists, err := instancesV2.InstanceExists(t.Context(), &nodeWithProviderID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Fatal("expected instance to exist via the Oxide API")
		}
	})

	t.Run("NoProviderID", func(t *testing.T) {
		instancesV2 := InstancesV2{
			client: &mockOxideClient{
				InstanceViewOutput: &instanceRunning,
			},
			project: "test",
		}
		exists, err := instancesV2.InstanceExists(t.Context(), &nodeWithoutProviderID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Fatal("expected instance to exist via the Oxide API")
		}
	})

	t.Run("DoesNotExistInOxide", func(t *testing.T) {
		instancesV2 := InstancesV2{
			client: &mockOxideClient{
				InstanceViewError: oxide.ErrObjectNotFound,
			},
			project: "test",
		}
		exists, err := instancesV2.InstanceExists(t.Context(), &nodeDoesNotExistInOxide)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Fatal("expected instance to NOT exist via the Oxide API")
		}
	})
}

func TestShutdown(t *testing.T) {
	t.Run("RunningWithProviderID", func(t *testing.T) {
		instancesV2 := InstancesV2{
			client: &mockOxideClient{
				InstanceViewOutput: &instanceRunning,
			},
			project: "test",
		}
		shutdown, err := instancesV2.InstanceShutdown(t.Context(), &nodeWithProviderID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if shutdown {
			t.Fatal("expected instance to be running via the Oxide API")
		}
	})

	t.Run("RunningNoProviderID", func(t *testing.T) {
		instancesV2 := InstancesV2{
			client: &mockOxideClient{
				InstanceViewOutput: &instanceRunning,
			},
			project: "test",
		}
		shutdown, err := instancesV2.InstanceShutdown(t.Context(), &nodeWithoutProviderID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if shutdown {
			t.Fatal("expected instance to be running via the Oxide API")
		}
	})

	t.Run("InstanceStopped", func(t *testing.T) {
		instancesV2 := InstancesV2{
			client: &mockOxideClient{
				InstanceViewOutput: &instanceStopped,
			},
			project: "test",
		}
		shutdown, err := instancesV2.InstanceShutdown(t.Context(), &nodeDoesNotExistInOxide)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !shutdown {
			t.Fatal("expected instance to be stopped via the Oxide API")
		}
	})

	t.Run("DoesNotExistInOxide", func(t *testing.T) {
		instancesV2 := InstancesV2{
			client: &mockOxideClient{
				InstanceViewError: oxide.ErrObjectNotFound,
			},
			project: "test",
		}
		shutdown, err := instancesV2.InstanceShutdown(t.Context(), &nodeDoesNotExistInOxide)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !shutdown {
			t.Fatal("expected instance to NOT exist via the Oxide API")
		}
	})
}

func (c *mockOxideClient) InstanceNetworkInterfaceList(
	context.Context,
	oxide.InstanceNetworkInterfaceListParams,
) (*oxide.InstanceNetworkInterfaceResultsPage, error) {
	if c.InstanceNetworkInterfaceListError != nil {
		return nil, c.InstanceNetworkInterfaceListError
	}
	return c.InstanceNetworkInterfaceListOutput, nil
}

func (c *mockOxideClient) InstanceExternalIpList(
	context.Context,
	oxide.InstanceExternalIpListParams,
) (*oxide.ExternalIpResultsPage, error) {
	if c.InstanceExternalIpListError != nil {
		return nil, c.InstanceExternalIpListError
	}
	return c.InstanceExternalIpListOutput, nil
}

func (c *mockOxideClient) InstanceView(
	context.Context,
	oxide.InstanceViewParams,
) (*oxide.Instance, error) {
	if c.InstanceViewError != nil {
		return nil, c.InstanceViewError
	}
	return c.InstanceViewOutput, nil
}
