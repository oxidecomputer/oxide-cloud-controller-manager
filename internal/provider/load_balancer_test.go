// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/oxidecomputer/oxide.go/oxide"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// Test infrastructure: fakes and helpers shared across the tests below.

// Instance IDs used by tests that exercise InstanceIDFromProviderID, which
// requires valid UUIDs.
const (
	instID1   = "11111111-1111-1111-1111-111111111111"
	instIDOld = "22222222-2222-2222-2222-222222222222"
	instIDNew = "33333333-3333-3333-3333-333333333333"
)

// testFloatingIP is the floating IP address advertised by every fake floating
// IP in these tests.
const testFloatingIP = "203.0.113.10"

// errUnexpectedOxideCall is returned by the fake Oxide client when a method is
// invoked that the test did not configure. It makes unexpected API calls fail
// loudly rather than silently succeeding.
var errUnexpectedOxideCall = errors.New("unexpected oxide client call")

// errBoom is the canned failure returned by fakes in error-path tests. Tests
// assert with errors.Is(err, errBoom) so a test only passes when the error
// actually originated from the call under test, not from some earlier
// validation that happens to also fail.
var errBoom = errors.New("boom")

// fakeOxideLBClient is a configurable mock of [oxideLoadBalancerClient]. Each
// method delegates to its function field; an unset field fails the call.
type fakeOxideLBClient struct {
	FloatingIpViewFn func(
		context.Context, oxide.FloatingIpViewParams,
	) (*oxide.FloatingIp, error)
	FloatingIpCreateFn func(
		context.Context, oxide.FloatingIpCreateParams,
	) (*oxide.FloatingIp, error)
	FloatingIpDeleteFn func(
		context.Context, oxide.FloatingIpDeleteParams,
	) error
	FloatingIpAttachFn func(
		context.Context, oxide.FloatingIpAttachParams,
	) (*oxide.FloatingIp, error)
	FloatingIpDetachFn func(
		context.Context, oxide.FloatingIpDetachParams,
	) (*oxide.FloatingIp, error)
	IpPoolViewFn func(
		context.Context, oxide.IpPoolViewParams,
	) (*oxide.SiloIpPool, error)
}

func (f *fakeOxideLBClient) FloatingIpView(
	ctx context.Context, p oxide.FloatingIpViewParams,
) (*oxide.FloatingIp, error) {
	if f.FloatingIpViewFn == nil {
		return nil, errUnexpectedOxideCall
	}
	return f.FloatingIpViewFn(ctx, p)
}

func (f *fakeOxideLBClient) FloatingIpCreate(
	ctx context.Context, p oxide.FloatingIpCreateParams,
) (*oxide.FloatingIp, error) {
	if f.FloatingIpCreateFn == nil {
		return nil, errUnexpectedOxideCall
	}
	return f.FloatingIpCreateFn(ctx, p)
}

func (f *fakeOxideLBClient) FloatingIpDelete(
	ctx context.Context, p oxide.FloatingIpDeleteParams,
) error {
	if f.FloatingIpDeleteFn == nil {
		return errUnexpectedOxideCall
	}
	return f.FloatingIpDeleteFn(ctx, p)
}

func (f *fakeOxideLBClient) FloatingIpAttach(
	ctx context.Context, p oxide.FloatingIpAttachParams,
) (*oxide.FloatingIp, error) {
	if f.FloatingIpAttachFn == nil {
		return nil, errUnexpectedOxideCall
	}
	return f.FloatingIpAttachFn(ctx, p)
}

func (f *fakeOxideLBClient) FloatingIpDetach(
	ctx context.Context, p oxide.FloatingIpDetachParams,
) (*oxide.FloatingIp, error) {
	if f.FloatingIpDetachFn == nil {
		return nil, errUnexpectedOxideCall
	}
	return f.FloatingIpDetachFn(ctx, p)
}

func (f *fakeOxideLBClient) IpPoolView(
	ctx context.Context, p oxide.IpPoolViewParams,
) (*oxide.SiloIpPool, error) {
	if f.IpPoolViewFn == nil {
		return nil, errUnexpectedOxideCall
	}
	return f.IpPoolViewFn(ctx, p)
}

// newLBService builds a LoadBalancer-type service named "ns/svc" with the
// Cluster external traffic policy that EnsureLoadBalancer requires.
func newLBService(annotations map[string]string) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "ns",
			Name:        "svc",
			Annotations: annotations,
		},
		Spec: v1.ServiceSpec{
			Type:                  v1.ServiceTypeLoadBalancer,
			ExternalTrafficPolicy: v1.ServiceExternalTrafficPolicyCluster,
		},
	}
}

// newLBNode builds a node whose provider ID maps to instanceID and that
// advertises internalIP as its internal address.
func newLBNode(name, instanceID, internalIP string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1.NodeSpec{ProviderID: NewProviderID(instanceID)},
		Status: v1.NodeStatus{
			Addresses: []v1.NodeAddress{
				{Type: v1.NodeInternalIP, Address: internalIP},
			},
		},
	}
}

// serviceWithIngressIP builds a service whose load balancer status advertises
// the floating IP plus the given node internal IP.
func serviceWithIngressIP(ip string) *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "svc",
		},
		Spec: v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
		Status: v1.ServiceStatus{
			LoadBalancer: v1.LoadBalancerStatus{
				Ingress: []v1.LoadBalancerIngress{
					{IP: "203.0.113.10", IPMode: new(v1.LoadBalancerIPModeProxy)},
					{IP: ip},
				},
			},
		},
	}
}

// assertProxyAndNodeIngress asserts that ingress advertises exactly the
// floating IP (as a Proxy-mode entry) followed by the node's internal IP (with
// no IP mode). This is the shape every successful status must take, so the
// proxy floating IP is checked alongside the node IP rather than the node IP
// alone.
func assertProxyAndNodeIngress(
	t *testing.T, ingress []v1.LoadBalancerIngress, nodeIP string,
) {
	t.Helper()
	if len(ingress) != 2 {
		t.Fatalf("ingress = %+v, want floating ip + node ip", ingress)
	}
	if ingress[0].IP != testFloatingIP {
		t.Fatalf("floating ip ingress = %q, want %q",
			ingress[0].IP, testFloatingIP,
		)
	}
	if ingress[0].IPMode == nil ||
		*ingress[0].IPMode != v1.LoadBalancerIPModeProxy {
		t.Fatalf("floating ip ingress IPMode = %v, want Proxy", ingress[0].IPMode)
	}
	if ingress[1].IP != nodeIP {
		t.Fatalf("node ingress = %q, want %q", ingress[1].IP, nodeIP)
	}
	if ingress[1].IPMode != nil {
		t.Fatalf("node ingress IPMode = %v, want nil", ingress[1].IPMode)
	}
}

// Exported method tests.

func TestGetLoadBalancer(t *testing.T) {
	t.Run("FloatingIPNotFound", func(t *testing.T) {
		lb := &LoadBalancer{
			project:   "test",
			k8sClient: fake.NewSimpleClientset(),
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return nil, oxide.ErrObjectNotFound
				},
			},
		}

		status, exists, err := lb.GetLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
		)
		if err != nil || exists || status != nil {
			t.Fatalf("got (%v, %v, %v), want (nil, false, nil)",
				status, exists, err,
			)
		}
	})

	t.Run("FloatingIPViewError", func(t *testing.T) {
		lb := &LoadBalancer{
			project:   "test",
			k8sClient: fake.NewSimpleClientset(),
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return nil, errBoom
				},
			},
		}

		_, exists, err := lb.GetLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom", err)
		}
		if exists {
			t.Fatal("expected exists=false on error")
		}
	})

	t.Run("NotAttached", func(t *testing.T) {
		lb := &LoadBalancer{
			project:   "test",
			k8sClient: fake.NewSimpleClientset(),
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Ip: "203.0.113.10"}, nil
				},
			},
		}

		status, exists, err := lb.GetLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
		)
		if err != nil || !exists {
			t.Fatalf("got (exists=%v, err=%v), want (true, nil)", exists, err)
		}
		if len(status.Ingress) != 1 || status.Ingress[0].IP != "203.0.113.10" {
			t.Fatalf("ingress = %+v, want only the floating ip", status.Ingress)
		}
	})

	t.Run("AttachedNodeFound", func(t *testing.T) {
		lb := &LoadBalancer{
			project: "test",
			k8sClient: fake.NewSimpleClientset(
				newLBNode("node-a", instID1, "10.0.0.5"),
			),
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Ip: "203.0.113.10", InstanceId: instID1,
					}, nil
				},
			},
		}

		status, exists, err := lb.GetLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
		)
		if err != nil || !exists {
			t.Fatalf("got (exists=%v, err=%v), want (true, nil)", exists, err)
		}
		assertProxyAndNodeIngress(t, status.Ingress, "10.0.0.5")
	})

	t.Run("AttachedNonClusterInstance", func(t *testing.T) {
		// Floating IP is attached to an instance whose node is gone (failover
		// window). The load balancer still exists; report just the floating IP.
		lb := &LoadBalancer{
			project:   "test",
			k8sClient: fake.NewSimpleClientset(),
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Ip: "203.0.113.10", InstanceId: "ghost",
					}, nil
				},
			},
		}

		status, exists, err := lb.GetLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Fatal("expected exists=true for an existing floating ip")
		}
		if len(status.Ingress) != 1 || status.Ingress[0].IP != "203.0.113.10" {
			t.Fatalf("ingress = %+v, want only the floating ip", status.Ingress)
		}
	})

	t.Run("NodeListError", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.PrependReactor("list", "nodes", func(
			k8stesting.Action,
		) (bool, runtime.Object, error) {
			return true, nil, errBoom
		})

		lb := &LoadBalancer{
			project:   "test",
			k8sClient: client,
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Ip: "203.0.113.10", InstanceId: instID1,
					}, nil
				},
			},
		}

		_, _, err := lb.GetLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from node list", err)
		}
	})
}

func TestGetLoadBalancerName(t *testing.T) {
	t.Run("Composed", func(t *testing.T) {
		lb := &LoadBalancer{}
		got := lb.GetLoadBalancerName(
			t.Context(), "cluster",
			newLBService(nil),
		)
		if got != "cluster-ns-svc" {
			t.Fatalf("name = %q, want %q", got, "cluster-ns-svc")
		}
	})

	t.Run("TruncatedTo63", func(t *testing.T) {
		lb := &LoadBalancer{}
		got := lb.GetLoadBalancerName(
			t.Context(), strings.Repeat("a", 70),
			newLBService(nil),
		)
		if len(got) != 63 {
			t.Fatalf("len = %d, want 63", len(got))
		}
	})

	t.Run("TrailingDashTrimmed", func(t *testing.T) {
		lb := &LoadBalancer{}
		// The 64th character (index 63) is the separator dash, so truncation
		// to 63 characters leaves a trailing dash that must be trimmed.
		got := lb.GetLoadBalancerName(
			t.Context(), strings.Repeat("a", 63),
			newLBService(nil),
		)
		if strings.HasSuffix(got, "-") {
			t.Fatalf("name %q has trailing dash", got)
		}
		if got != strings.Repeat("a", 63) {
			t.Fatalf("name = %q, want 63 a's", got)
		}
	})
}

func TestEnsureLoadBalancer(t *testing.T) {
	node := newLBNode("node-a", instID1, "10.0.0.5")

	t.Run("UnsupportedExternalTrafficPolicy", func(t *testing.T) {
		svc := newLBService(nil)
		svc.Spec.ExternalTrafficPolicy = v1.ServiceExternalTrafficPolicyLocal
		lb := &LoadBalancer{project: "test", client: &fakeOxideLBClient{}}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", svc, []*v1.Node{node},
		)
		if err == nil {
			t.Fatal("expected error for non-Cluster external traffic policy")
		}
	})

	t.Run("NoNodes", func(t *testing.T) {
		lb := &LoadBalancer{project: "test", client: &fakeOxideLBClient{}}
		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", newLBService(nil), nil,
		)
		if err == nil {
			t.Fatal("expected error for empty nodes")
		}
	})

	t.Run("InvalidProviderID", func(t *testing.T) {
		bad := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			Spec:       v1.NodeSpec{ProviderID: "not-oxide"},
		}
		lb := &LoadBalancer{project: "test", client: &fakeOxideLBClient{}}
		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
			[]*v1.Node{bad},
		)
		if err == nil {
			t.Fatal("expected error for invalid provider id")
		}
	})

	t.Run("InvalidAnnotations", func(t *testing.T) {
		svc := newLBService(map[string]string{
			AnnotationFloatingIP:     "203.0.113.10",
			AnnotationFloatingIPPool: "external",
		})
		lb := &LoadBalancer{project: "test", client: &fakeOxideLBClient{}}
		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", svc, []*v1.Node{node},
		)
		if err == nil {
			t.Fatal("expected error for mutually exclusive annotations")
		}
	})

	t.Run("CreatesAndAttaches", func(t *testing.T) {
		var attachedTo oxide.NameOrId
		created := false
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return nil, oxide.ErrObjectNotFound
				},
				FloatingIpCreateFn: func(
					context.Context, oxide.FloatingIpCreateParams,
				) (*oxide.FloatingIp, error) {
					created = true
					return &oxide.FloatingIp{Id: "fip-1", Ip: "203.0.113.10"}, nil
				},
				FloatingIpAttachFn: func(
					_ context.Context, p oxide.FloatingIpAttachParams,
				) (*oxide.FloatingIp, error) {
					attachedTo = p.Body.Parent
					return &oxide.FloatingIp{
						Id: "fip-1", Ip: "203.0.113.10", InstanceId: instID1,
					}, nil
				},
			},
		}

		status, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
			[]*v1.Node{node},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !created {
			t.Fatal("expected floating ip to be created")
		}
		if string(attachedTo) != instID1 {
			t.Fatalf("attached to %q, want %q", attachedTo, instID1)
		}
		assertProxyAndNodeIngress(t, status.Ingress, "10.0.0.5")
	})

	t.Run("ReusesExisting", func(t *testing.T) {
		// View returns a matching floating IP, so Create/Delete must not be
		// called (their nil func fields would error if they were).
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-1", Ip: "203.0.113.10"}, nil
				},
				FloatingIpAttachFn: func(
					context.Context, oxide.FloatingIpAttachParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-1", Ip: "203.0.113.10", InstanceId: instID1,
					}, nil
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
			[]*v1.Node{node},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("RecreatesOnExplicitIPChange", func(t *testing.T) {
		deleted := false
		created := false
		svc := newLBService(map[string]string{
			AnnotationFloatingIP: "203.0.113.99",
		})
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-old", Ip: "203.0.113.10"}, nil
				},
				FloatingIpDeleteFn: func(
					context.Context, oxide.FloatingIpDeleteParams,
				) error {
					deleted = true
					return nil
				},
				FloatingIpCreateFn: func(
					context.Context, oxide.FloatingIpCreateParams,
				) (*oxide.FloatingIp, error) {
					created = true
					return &oxide.FloatingIp{Id: "fip-new", Ip: "203.0.113.99"}, nil
				},
				FloatingIpAttachFn: func(
					context.Context, oxide.FloatingIpAttachParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-new", Ip: "203.0.113.99", InstanceId: instID1,
					}, nil
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", svc, []*v1.Node{node},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !deleted || !created {
			t.Fatalf("expected recreate (deleted=%v, created=%v)", deleted, created)
		}
	})

	t.Run("RecreatesOnPoolChange", func(t *testing.T) {
		deleted := false
		svc := newLBService(map[string]string{
			AnnotationFloatingIPPool: "external",
		})
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-old", Ip: "203.0.113.10", IpPoolId: "pool-old",
					}, nil
				},
				IpPoolViewFn: func(
					context.Context, oxide.IpPoolViewParams,
				) (*oxide.SiloIpPool, error) {
					return &oxide.SiloIpPool{Id: "pool-new"}, nil
				},
				FloatingIpDeleteFn: func(
					context.Context, oxide.FloatingIpDeleteParams,
				) error {
					deleted = true
					return nil
				},
				FloatingIpCreateFn: func(
					context.Context, oxide.FloatingIpCreateParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-new", Ip: "203.0.113.11"}, nil
				},
				FloatingIpAttachFn: func(
					context.Context, oxide.FloatingIpAttachParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-new", Ip: "203.0.113.11", InstanceId: instID1,
					}, nil
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", svc, []*v1.Node{node},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !deleted {
			t.Fatal("expected floating ip to be recreated on pool change")
		}
	})

	t.Run("AttachError", func(t *testing.T) {
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return nil, oxide.ErrObjectNotFound
				},
				FloatingIpCreateFn: func(
					context.Context, oxide.FloatingIpCreateParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-1", Ip: "203.0.113.10"}, nil
				},
				FloatingIpAttachFn: func(
					context.Context, oxide.FloatingIpAttachParams,
				) (*oxide.FloatingIp, error) {
					return nil, errBoom
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
			[]*v1.Node{node},
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from attach", err)
		}
	})

	t.Run("ViewError", func(t *testing.T) {
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return nil, errBoom
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", newLBService(nil), []*v1.Node{node},
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from view", err)
		}
	})

	t.Run("CreateError", func(t *testing.T) {
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return nil, oxide.ErrObjectNotFound
				},
				FloatingIpCreateFn: func(
					context.Context, oxide.FloatingIpCreateParams,
				) (*oxide.FloatingIp, error) {
					return nil, errBoom
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", newLBService(nil), []*v1.Node{node},
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from create", err)
		}
	})

	t.Run("PoolViewError", func(t *testing.T) {
		svc := newLBService(map[string]string{
			AnnotationFloatingIPPool: "external",
		})
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-1", Ip: "203.0.113.10", IpPoolId: "pool-old",
					}, nil
				},
				IpPoolViewFn: func(
					context.Context, oxide.IpPoolViewParams,
				) (*oxide.SiloIpPool, error) {
					return nil, errBoom
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", svc, []*v1.Node{node},
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from ip pool view", err)
		}
	})

	t.Run("RecreateDetachError", func(t *testing.T) {
		// Explicit IP change forces a recreate; the floating IP is attached, so
		// the detach during recreate is exercised and made to fail.
		svc := newLBService(map[string]string{
			AnnotationFloatingIP: "203.0.113.99",
		})
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-old", Ip: "203.0.113.10", InstanceId: instIDOld,
					}, nil
				},
				FloatingIpDetachFn: func(
					context.Context, oxide.FloatingIpDetachParams,
				) (*oxide.FloatingIp, error) {
					return nil, errBoom
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", svc, []*v1.Node{node},
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from detach during recreate", err)
		}
	})

	t.Run("RecreateDeleteError", func(t *testing.T) {
		svc := newLBService(map[string]string{
			AnnotationFloatingIP: "203.0.113.99",
		})
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-old", Ip: "203.0.113.10"}, nil
				},
				FloatingIpDeleteFn: func(
					context.Context, oxide.FloatingIpDeleteParams,
				) error {
					return errBoom
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", svc, []*v1.Node{node},
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from delete during recreate", err)
		}
	})

	t.Run("RecreatesOnIPVersionChange", func(t *testing.T) {
		// Existing floating IP is IPv4 but the annotation requests v6, so the
		// IP-version branch of floatingIPNeedsRecreate must trigger a recreate.
		deleted := false
		svc := newLBService(map[string]string{
			AnnotationFloatingIPVersion: "v6",
		})
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-old", Ip: "203.0.113.10"}, nil
				},
				FloatingIpDeleteFn: func(
					context.Context, oxide.FloatingIpDeleteParams,
				) error {
					deleted = true
					return nil
				},
				FloatingIpCreateFn: func(
					context.Context, oxide.FloatingIpCreateParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-new", Ip: "2001:db8::1"}, nil
				},
				FloatingIpAttachFn: func(
					context.Context, oxide.FloatingIpAttachParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-new", Ip: "2001:db8::1", InstanceId: instID1,
					}, nil
				},
			},
		}

		_, err := lb.EnsureLoadBalancer(
			t.Context(), "cluster", svc, []*v1.Node{node},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !deleted {
			t.Fatal("expected recreate on ip version change")
		}
	})
}

func TestUpdateLoadBalancer(t *testing.T) {
	t.Run("NoNodes", func(t *testing.T) {
		lb := &LoadBalancer{project: "test", client: &fakeOxideLBClient{}}
		err := lb.UpdateLoadBalancer(
			t.Context(), "cluster", newLBService(nil), nil,
		)
		if err == nil {
			t.Fatal("expected error for empty nodes")
		}
	})

	t.Run("InvalidProviderID", func(t *testing.T) {
		bad := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			Spec:       v1.NodeSpec{ProviderID: "not-oxide"},
		}
		lb := &LoadBalancer{project: "test", client: &fakeOxideLBClient{}}
		err := lb.UpdateLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
			[]*v1.Node{bad},
		)
		if err == nil {
			t.Fatal("expected error for invalid provider id")
		}
	})

	t.Run("FloatingIPViewError", func(t *testing.T) {
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return nil, errBoom
				},
			},
		}
		err := lb.UpdateLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
			[]*v1.Node{newLBNode("node-a", instID1, "10.0.0.5")},
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from view", err)
		}
	})

	t.Run("AlreadyAttachedPatchesStatus", func(t *testing.T) {
		svc := newLBService(nil)
		client := fake.NewSimpleClientset(svc)
		lb := &LoadBalancer{
			project:   "test",
			k8sClient: client,
			client: &fakeOxideLBClient{
				// Attach/Detach funcs are nil: if either is called the test fails.
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-1", Ip: "203.0.113.10", InstanceId: instID1,
					}, nil
				},
			},
		}

		err := lb.UpdateLoadBalancer(
			t.Context(), "cluster", svc,
			[]*v1.Node{newLBNode("node-a", instID1, "10.0.0.5")},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, _ := client.CoreV1().Services("ns").Get(
			t.Context(), "svc", metav1.GetOptions{},
		)
		assertProxyAndNodeIngress(t, got.Status.LoadBalancer.Ingress, "10.0.0.5")
	})

	t.Run("FailoverDetachesAndReattaches", func(t *testing.T) {
		svc := newLBService(nil)
		svc.Status.LoadBalancer = v1.LoadBalancerStatus{
			Ingress: []v1.LoadBalancerIngress{
				{IP: "203.0.113.10", IPMode: new(v1.LoadBalancerIPModeProxy)},
				{IP: "10.0.0.10"}, // stale, destroyed node
			},
		}
		client := fake.NewSimpleClientset(svc)

		detached := false
		var attachedTo oxide.NameOrId
		lb := &LoadBalancer{
			project:   "test",
			k8sClient: client,
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-1", Ip: "203.0.113.10", InstanceId: instIDOld,
					}, nil
				},
				FloatingIpDetachFn: func(
					context.Context, oxide.FloatingIpDetachParams,
				) (*oxide.FloatingIp, error) {
					detached = true
					return &oxide.FloatingIp{Id: "fip-1", Ip: "203.0.113.10"}, nil
				},
				FloatingIpAttachFn: func(
					_ context.Context, p oxide.FloatingIpAttachParams,
				) (*oxide.FloatingIp, error) {
					attachedTo = p.Body.Parent
					return &oxide.FloatingIp{
						Id: "fip-1", Ip: "203.0.113.10", InstanceId: instIDNew,
					}, nil
				},
			},
		}

		err := lb.UpdateLoadBalancer(
			t.Context(), "cluster", svc,
			[]*v1.Node{newLBNode("node-b", instIDNew, "10.0.0.20")},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !detached {
			t.Fatal("expected floating ip to be detached from old instance")
		}
		if string(attachedTo) != instIDNew {
			t.Fatalf("attached to %q, want %q", attachedTo, instIDNew)
		}

		// The persisted status must advertise the new node's internal IP.
		got, _ := client.CoreV1().Services("ns").Get(
			t.Context(), "svc", metav1.GetOptions{},
		)
		assertProxyAndNodeIngress(t, got.Status.LoadBalancer.Ingress, "10.0.0.20")

		// UpdateLoadBalancer must treat the service parameter as read-only, so
		// the in-memory svc must still carry the original stale status.
		if svc.Status.LoadBalancer.Ingress[1].IP != "10.0.0.10" {
			t.Fatalf(
				"service parameter was mutated, node ingress ip = %q, want %q",
				svc.Status.LoadBalancer.Ingress[1].IP, "10.0.0.10",
			)
		}
	})

	t.Run("DetachError", func(t *testing.T) {
		lb := &LoadBalancer{
			project:   "test",
			k8sClient: fake.NewSimpleClientset(),
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-1", Ip: "203.0.113.10", InstanceId: instIDOld,
					}, nil
				},
				FloatingIpDetachFn: func(
					context.Context, oxide.FloatingIpDetachParams,
				) (*oxide.FloatingIp, error) {
					return nil, errBoom
				},
			},
		}

		err := lb.UpdateLoadBalancer(
			t.Context(), "cluster", newLBService(nil),
			[]*v1.Node{newLBNode("node-b", instIDNew, "10.0.0.20")},
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from detach", err)
		}
	})

	t.Run("ToleratesMissingService", func(t *testing.T) {
		// Service is not in the cluster (deleted concurrently); the status
		// patch should be tolerated and the update succeed.
		svc := newLBService(nil)
		lb := &LoadBalancer{
			project:   "test",
			k8sClient: fake.NewSimpleClientset(),
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{
						Id: "fip-1", Ip: "203.0.113.10", InstanceId: instID1,
					}, nil
				},
			},
		}

		err := lb.UpdateLoadBalancer(
			t.Context(), "cluster", svc,
			[]*v1.Node{newLBNode("node-a", instID1, "10.0.0.5")},
		)
		if err != nil {
			t.Fatalf("expected nil error for missing service, got: %v", err)
		}
	})
}

func TestEnsureLoadBalancerDeleted(t *testing.T) {
	t.Run("NotFoundIsIdempotent", func(t *testing.T) {
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return nil, oxide.ErrObjectNotFound
				},
			},
		}

		err := lb.EnsureLoadBalancerDeleted(
			t.Context(), "cluster", newLBService(nil),
		)
		if err != nil {
			t.Fatalf("expected nil error for missing floating ip, got: %v", err)
		}
	})

	t.Run("ViewError", func(t *testing.T) {
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return nil, errBoom
				},
			},
		}

		err := lb.EnsureLoadBalancerDeleted(
			t.Context(), "cluster", newLBService(nil),
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from view", err)
		}
	})

	t.Run("AttachedDetachesThenDeletes", func(t *testing.T) {
		detached := false
		deleted := false
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-1", InstanceId: instID1}, nil
				},
				FloatingIpDetachFn: func(
					context.Context, oxide.FloatingIpDetachParams,
				) (*oxide.FloatingIp, error) {
					detached = true
					return &oxide.FloatingIp{Id: "fip-1"}, nil
				},
				FloatingIpDeleteFn: func(
					context.Context, oxide.FloatingIpDeleteParams,
				) error {
					deleted = true
					return nil
				},
			},
		}

		err := lb.EnsureLoadBalancerDeleted(
			t.Context(), "cluster", newLBService(nil),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !detached || !deleted {
			t.Fatalf("expected detach+delete (detached=%v, deleted=%v)",
				detached, deleted,
			)
		}
	})

	t.Run("NotAttachedDeletesOnly", func(t *testing.T) {
		// Detach func is nil: if it is called, the test fails.
		deleted := false
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-1"}, nil
				},
				FloatingIpDeleteFn: func(
					context.Context, oxide.FloatingIpDeleteParams,
				) error {
					deleted = true
					return nil
				},
			},
		}

		err := lb.EnsureLoadBalancerDeleted(
			t.Context(), "cluster", newLBService(nil),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !deleted {
			t.Fatal("expected floating ip to be deleted")
		}
	})

	t.Run("DetachError", func(t *testing.T) {
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-1", InstanceId: instID1}, nil
				},
				FloatingIpDetachFn: func(
					context.Context, oxide.FloatingIpDetachParams,
				) (*oxide.FloatingIp, error) {
					return nil, errBoom
				},
			},
		}

		err := lb.EnsureLoadBalancerDeleted(
			t.Context(), "cluster", newLBService(nil),
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from detach", err)
		}
	})

	t.Run("DeleteError", func(t *testing.T) {
		lb := &LoadBalancer{
			project: "test",
			client: &fakeOxideLBClient{
				FloatingIpViewFn: func(
					context.Context, oxide.FloatingIpViewParams,
				) (*oxide.FloatingIp, error) {
					return &oxide.FloatingIp{Id: "fip-1"}, nil
				},
				FloatingIpDeleteFn: func(
					context.Context, oxide.FloatingIpDeleteParams,
				) error {
					return errBoom
				},
			},
		}

		err := lb.EnsureLoadBalancerDeleted(
			t.Context(), "cluster", newLBService(nil),
		)
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from delete", err)
		}
	})
}

// Internal method tests.

func TestSelectTargetNode(t *testing.T) {
	nodes := []*v1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "node-c"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "node-b"}},
	}

	target := selectTargetNode(nodes)
	if target.Name != "node-a" {
		t.Fatalf("target node = %q, want %q", target.Name, "node-a")
	}

	// The input slice must not be reordered.
	if nodes[0].Name != "node-c" {
		t.Fatalf("input slice was mutated, first node = %q, want %q",
			nodes[0].Name, "node-c",
		)
	}
}

func TestPatchServiceStatus(t *testing.T) {
	newStatus := &v1.LoadBalancerStatus{
		Ingress: []v1.LoadBalancerIngress{
			{IP: "203.0.113.10", IPMode: new(v1.LoadBalancerIPModeProxy)},
			{IP: "10.0.0.20"},
		},
	}

	t.Run("UpdatesStaleNodeIP", func(t *testing.T) {
		// The service status still advertises the destroyed node's IP.
		service := serviceWithIngressIP("10.0.0.10")
		client := fake.NewSimpleClientset(service)
		lb := &LoadBalancer{k8sClient: client}

		if err := lb.patchServiceStatus(service, newStatus); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// The persisted status must reflect the new node's internal IP.
		got, err := client.CoreV1().Services("default").Get(
			t.Context(), "svc", metav1.GetOptions{},
		)
		if err != nil {
			t.Fatalf("failed getting service: %v", err)
		}
		ingress := got.Status.LoadBalancer.Ingress
		if len(ingress) != 2 {
			t.Fatalf("ingress len = %d, want 2", len(ingress))
		}
		if ingress[1].IP != "10.0.0.20" {
			t.Fatalf("node ingress ip = %q, want %q",
				ingress[1].IP, "10.0.0.20",
			)
		}

		// The read-only service parameter must not have been mutated.
		if service.Status.LoadBalancer.Ingress[1].IP != "10.0.0.10" {
			t.Fatalf(
				"service parameter was mutated, node ingress ip = %q, want %q",
				service.Status.LoadBalancer.Ingress[1].IP, "10.0.0.10",
			)
		}
	})

	t.Run("NoOpWhenUnchanged", func(t *testing.T) {
		service := serviceWithIngressIP("10.0.0.20")
		client := fake.NewSimpleClientset(service)
		lb := &LoadBalancer{k8sClient: client}

		if err := lb.patchServiceStatus(service, newStatus); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, action := range client.Actions() {
			if action.Matches("patch", "services") {
				t.Fatal("expected no patch when status is unchanged")
			}
		}
	})

	t.Run("ToleratesMissingService", func(t *testing.T) {
		service := serviceWithIngressIP("10.0.0.10")
		// The service is absent from the cluster (deleted concurrently).
		client := fake.NewSimpleClientset()
		lb := &LoadBalancer{k8sClient: client}

		if err := lb.patchServiceStatus(service, newStatus); err != nil {
			t.Fatalf("expected nil error for missing service, got: %v", err)
		}
	})

	t.Run("PropagatesPatchError", func(t *testing.T) {
		// The service exists, so the patch is attempted, but the API returns a
		// non-NotFound error which patchServiceStatus must propagate.
		service := serviceWithIngressIP("10.0.0.10")
		client := fake.NewSimpleClientset(service)
		client.PrependReactor("patch", "services", func(
			k8stesting.Action,
		) (bool, runtime.Object, error) {
			return true, nil, errBoom
		})

		lb := &LoadBalancer{k8sClient: client}
		if err := lb.patchServiceStatus(service, newStatus); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom from patch", err)
		}
	})
}

func TestToLoadBalancerStatus(t *testing.T) {
	t.Run("WithInternalIPs", func(t *testing.T) {
		status := toLoadBalancerStatus(
			&oxide.FloatingIp{
				Ip: "203.0.113.10",
			},
			&v1.Node{
				Status: v1.NodeStatus{
					Addresses: []v1.NodeAddress{
						{
							Type:    v1.NodeInternalIP,
							Address: "10.0.0.10",
						},
					},
				},
			},
		)

		if len(status.Ingress) != 2 {
			t.Fatalf(
				"toLoadBalancerStatus returned %d ingress, want 2",
				len(status.Ingress),
			)
		}
		if status.Ingress[0].IP != "203.0.113.10" {
			t.Fatalf(
				"first ingress ip is %q, want %q",
				status.Ingress[0].IP, "203.0.113.10",
			)
		}
		if status.Ingress[0].IPMode == nil ||
			*status.Ingress[0].IPMode != v1.LoadBalancerIPModeProxy {
			t.Fatal(
				"first ingress IPMode should be Proxy",
			)
		}
		if status.Ingress[1].IP != "10.0.0.10" {
			t.Fatalf(
				"second ingress ip is %q, want %q",
				status.Ingress[1].IP, "10.0.0.10",
			)
		}
		if status.Ingress[1].IPMode != nil {
			t.Fatal(
				"second ingress IPMode should be nil",
			)
		}
	})

	t.Run("NoInternalIPs", func(t *testing.T) {
		status := toLoadBalancerStatus(
			&oxide.FloatingIp{
				Ip: "203.0.113.10",
			},
			nil,
		)

		if len(status.Ingress) != 1 {
			t.Fatalf(
				"toLoadBalancerStatus returned %d ingress, want 1",
				len(status.Ingress),
			)
		}
	})
}

func TestAddressAllocatorFromAnnotations(t *testing.T) {
	t.Run("NoAnnotations", func(t *testing.T) {
		alloc, err := addressAllocatorFromAnnotations(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if alloc.Type() != oxide.AddressAllocatorTypeAuto {
			t.Fatalf("type = %q, want %q",
				alloc.Type(), oxide.AddressAllocatorTypeAuto,
			)
		}
	})

	t.Run("ExplicitIP", func(t *testing.T) {
		alloc, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIP: "203.0.113.10",
			},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		explicit, ok := alloc.AsExplicit()
		if !ok {
			t.Fatal("expected explicit allocator")
		}
		if explicit.Ip != "203.0.113.10" {
			t.Fatalf("ip = %q, want %q",
				explicit.Ip, "203.0.113.10",
			)
		}
	})

	t.Run("ExplicitPool", func(t *testing.T) {
		alloc, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIPPool: "external",
			},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		auto, ok := alloc.AsAuto()
		if !ok {
			t.Fatal("expected auto allocator")
		}
		ps, ok := auto.PoolSelector.AsExplicit()
		if !ok {
			t.Fatal("expected explicit pool selector")
		}
		if string(ps.Pool) != "external" {
			t.Fatalf("pool = %q, want %q",
				ps.Pool, "external",
			)
		}
	})

	t.Run("IPVersion", func(t *testing.T) {
		alloc, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIPVersion: "v4",
			},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		auto, ok := alloc.AsAuto()
		if !ok {
			t.Fatal("expected auto allocator")
		}
		ps, ok := auto.PoolSelector.AsAuto()
		if !ok {
			t.Fatal("expected auto pool selector")
		}
		if ps.IpVersion != oxide.IpVersionV4 {
			t.Fatalf("ip_version = %q, want %q",
				ps.IpVersion, oxide.IpVersionV4,
			)
		}
	})

	t.Run("InvalidIPVersion", func(t *testing.T) {
		_, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIPVersion: "v5",
			},
		)
		if err == nil {
			t.Fatal("expected error for invalid ip version")
		}
	})

	t.Run("IPAndPoolMutuallyExclusive", func(t *testing.T) {
		_, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIP:     "203.0.113.10",
				AnnotationFloatingIPPool: "external",
			},
		)
		if err == nil {
			t.Fatal("expected error for mutually exclusive annotations")
		}
	})

	t.Run("PoolAndVersionMutuallyExclusive", func(t *testing.T) {
		_, err := addressAllocatorFromAnnotations(
			map[string]string{
				AnnotationFloatingIPPool:    "external",
				AnnotationFloatingIPVersion: "v4",
			},
		)
		if err == nil {
			t.Fatal("expected error for mutually exclusive annotations")
		}
	})
}
