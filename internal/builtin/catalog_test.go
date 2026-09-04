package builtin

import (
	"errors"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestDefaultCatalogIsShared(t *testing.T) {
	first, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("Default returned distinct immutable catalogs")
	}
}

type routeWithSpec struct {
	core.Route
	spec core.RouteSpec
}

func (r routeWithSpec) Specification() core.RouteSpec { return r.spec }

func TestCatalogRejectsMissingCodecFactory(t *testing.T) {
	if _, err := New(defaultRoutesForTest(t), nil); !errors.Is(err, core.ErrInvalidPlan) {
		t.Fatalf("error=%v, want ErrInvalidPlan", err)
	}
}

func TestCatalogRejectsRouteIdentityMismatch(t *testing.T) {
	routes := defaultRoutesForTest(t)
	spec := routes[0].Specification()
	spec.ID = "wrong_route"
	routes[0] = routeWithSpec{Route: routes[0], spec: spec}
	_, err := New(routes, func(core.Protocol) (core.Codec, error) { return nil, nil })
	if !errors.Is(err, core.ErrInvalidPlan) {
		t.Fatalf("error=%v, want ErrInvalidPlan", err)
	}
}

func TestExpectedRoutesCoverEveryOrderedProtocolPair(t *testing.T) {
	protocols := []core.Protocol{
		core.ProtocolChat,
		core.ProtocolResponses,
		core.ProtocolMessages,
		core.ProtocolGenerateContent,
	}
	wantCount := len(protocols) * (len(protocols) - 1)
	want := expectedRoutes()
	if len(want) != wantCount {
		t.Fatalf("route count=%d, want %d", len(want), wantCount)
	}
	for _, from := range protocols {
		for _, to := range protocols {
			_, found := want[routeKey{from: from, to: to}]
			if found != (from != to) {
				t.Fatalf("route %s -> %s found=%t, want %t", from, to, found, from != to)
			}
		}
	}
}

func defaultRoutesForTest(t *testing.T) []core.Route {
	t.Helper()
	routes := make([]core.Route, 0, 12)
	for _, spec := range defaultRouteSpecs() {
		route := newDefaultRoute(spec)
		if route == nil {
			t.Fatalf("default route %q is nil", spec.ID)
		}
		routes = append(routes, route)
	}
	return routes
}
