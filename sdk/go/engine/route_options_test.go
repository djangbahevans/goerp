package engine

import (
	"reflect"
	"testing"
	"time"
)

func TestNewRouteConfig_Defaults(t *testing.T) {
	c := newRouteConfig()

	want := routeConfig{
		auth:         AuthRequired,
		timeoutMs:    30000,
		maxBodyBytes: defaultMaxBodyBytes,
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
}

func TestAuth_Overrides(t *testing.T) {
	c := newRouteConfig(Auth(AuthNone))
	if c.auth != AuthNone {
		t.Fatalf("auth = %q, want %q", c.auth, AuthNone)
	}
}

func TestRequires_AccumulatesAcrossCalls(t *testing.T) {
	c := newRouteConfig(Requires("a:b"), Requires("c:d", "e:f"))
	want := []string{"a:b", "c:d", "e:f"}
	if !reflect.DeepEqual(c.permissions, want) {
		t.Fatalf("permissions = %+v, want %+v", c.permissions, want)
	}
}

func TestRateLimit_SetsDecl(t *testing.T) {
	c := newRouteConfig(RateLimit(100, 60, PerUser))
	want := &RateLimitDecl{Requests: 100, WindowSeconds: 60, Scope: PerUser}
	if !reflect.DeepEqual(c.rateLimit, want) {
		t.Fatalf("rateLimit = %+v, want %+v", c.rateLimit, want)
	}
}

func TestTimeout_ClampsToMaxAndZero(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want int
	}{
		{"within range", 10 * time.Second, 10000},
		{"above max clamps to 5m", 10 * time.Minute, int(maxTimeout / time.Millisecond)},
		{"negative clamps to zero", -1 * time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newRouteConfig(Timeout(tc.in))
			if c.timeoutMs != tc.want {
				t.Fatalf("timeoutMs = %d, want %d", c.timeoutMs, tc.want)
			}
		})
	}
}

func TestMaxBody_ZeroIsDistinctFromUnset(t *testing.T) {
	unset := newRouteConfig()
	if unset.maxBodyBytes != defaultMaxBodyBytes {
		t.Fatalf("unset maxBodyBytes = %d, want default %d", unset.maxBodyBytes, defaultMaxBodyBytes)
	}

	explicitZero := newRouteConfig(MaxBody(0))
	if explicitZero.maxBodyBytes != 0 {
		t.Fatalf("explicit MaxBody(0) = %d, want 0", explicitZero.maxBodyBytes)
	}
}

func TestRawBody_And_Streaming(t *testing.T) {
	c := newRouteConfig(RawBody(), Streaming())
	if !c.rawBody {
		t.Error("rawBody = false, want true")
	}
	if !c.streaming {
		t.Error("streaming = false, want true")
	}
}

func TestEmbeds_AccumulatesAcrossCalls(t *testing.T) {
	c := newRouteConfig(
		Embeds("lines", "sales.order_line", true),
		Embeds("customer", "contacts.contact", false),
	)
	want := []EmbeddedDecl{
		{Field: "lines", Resource: "sales.order_line", IsList: true},
		{Field: "customer", Resource: "contacts.contact", IsList: false},
	}
	if !reflect.DeepEqual(c.embedded, want) {
		t.Fatalf("embedded = %+v, want %+v", c.embedded, want)
	}
}

func TestPathParam_AccumulatesAcrossCalls(t *testing.T) {
	c := newRouteConfig(
		PathParam("id", UUIDParam),
		PathParam("slug", SlugParam),
	)
	want := map[string]string{"id": "uuid", "slug": "slug"}
	if !reflect.DeepEqual(c.pathParams, want) {
		t.Fatalf("pathParams = %+v, want %+v", c.pathParams, want)
	}
}

func TestQueryParam_AccumulatesAcrossCalls(t *testing.T) {
	max := 200
	c := newRouteConfig(
		QueryParam("q", QueryString),
		QueryParam("limit", QueryInteger, QueryDefault(50), QueryMax(200)),
		QueryParam("filter[type]", QueryString, QueryEnum("person", "company")),
	)
	want := map[string]QueryParamDecl{
		"q":            {Type: "string"},
		"limit":        {Type: "integer", Default: 50, Max: &max},
		"filter[type]": {Type: "string", Enum: []string{"person", "company"}},
	}
	if !reflect.DeepEqual(c.queryParams, want) {
		t.Fatalf("queryParams = %+v, want %+v", c.queryParams, want)
	}
}

func TestQueryParam_LaterCallOverridesSameName(t *testing.T) {
	c := newRouteConfig(
		QueryParam("q", QueryString),
		QueryParam("q", QueryInteger),
	)
	want := map[string]QueryParamDecl{"q": {Type: "integer"}}
	if !reflect.DeepEqual(c.queryParams, want) {
		t.Fatalf("queryParams = %+v, want %+v", c.queryParams, want)
	}
}
