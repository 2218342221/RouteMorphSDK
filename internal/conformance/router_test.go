package conformance

import "context"

// These fixtures keep protocol characterization tests concise while exercising
// the same private router used by the public Adapter facade.
type testRoutePlan struct {
	From      Protocol
	To        Protocol
	RouteIDs  []string
	RouteMode RouteMode
	Plan      routePlan
}

type testRequestExecution struct {
	Plan   testRoutePlan
	Result conversionResult
}

type testRouteCatalog struct{ router *router }

func newTestRouteCatalog() (*testRouteCatalog, error) {
	router, err := newBuiltinRouter()
	return &testRouteCatalog{router: router}, err
}

func (r *testRouteCatalog) Plan(from, to Protocol) (testRoutePlan, error) {
	plan, err := r.router.Plan(from, to)
	if err != nil {
		return testRoutePlan{}, err
	}
	ids := []string(nil)
	if plan.Converter != nil {
		ids = []string{plan.Converter.Specification().ID}
	}
	return testRoutePlan{From: from, To: to, RouteIDs: ids, RouteMode: plan.StreamMode, Plan: plan}, nil
}

type testRouterHarness struct{ router *router }

func newTestRouterHarness() (*testRouterHarness, error) {
	router, err := newBuiltinRouter()
	return &testRouterHarness{router: router}, err
}

func (e *testRouterHarness) catalog() *testRouteCatalog {
	return &testRouteCatalog{router: e.router}
}

func (e *testRouterHarness) ToUpstreamRequest(ctx context.Context, from, to Protocol, body []byte, options conversionOptions) (testRequestExecution, error) {
	plan, err := e.catalog().Plan(from, to)
	if err != nil {
		return testRequestExecution{}, err
	}
	result, err := e.router.ConvertRequest(ctx, plan.Plan, body, options)
	return testRequestExecution{Plan: plan, Result: result}, err
}

func (e *testRouterHarness) ToClientResponse(ctx context.Context, plan testRoutePlan, body []byte, options conversionOptions) (conversionResult, error) {
	return e.router.ConvertResponse(ctx, plan.Plan, body, options)
}

func (e *testRouterHarness) NewResponseStream(ctx context.Context, plan testRoutePlan, options conversionOptions) (responseStreamConverter, error) {
	return e.router.NewResponseStream(ctx, plan.Plan, options)
}
