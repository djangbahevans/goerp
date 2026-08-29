package route

import "testing"

func TestRegisterModuleRoutes_RootExpansion(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/"},
	})
	if err != nil {
		t.Fatalf("RegisterModuleRoutes: %v", err)
	}

	_, _, result, _ := table.Lookup("GET", "/contacts")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
}

func TestRegisterModuleRoutes_ParamExpansion(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/{id}"},
	})
	if err != nil {
		t.Fatalf("RegisterModuleRoutes: %v", err)
	}

	entry, params, result, _ := table.Lookup("GET", "/contacts/123")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
	if entry.PathTemplate != "/contacts/{id}" {
		t.Fatalf("PathTemplate = %q, want %q", entry.PathTemplate, "/contacts/{id}")
	}
	if params["id"] != "123" {
		t.Fatalf("params[id] = %q, want %q", params["id"], "123")
	}
}

func TestRegisterModuleRoutes_ConnectorExpansion(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "connector_paystack", "connector", []ExplicitRoute{
		{Method: "GET", Path: "/status"},
	})
	if err != nil {
		t.Fatalf("RegisterModuleRoutes: %v", err)
	}

	_, _, result, _ := table.Lookup("GET", "/connectors/connector_paystack/status")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}

	// A connector's routes never expand under its own bare module-name prefix.
	_, _, result, _ = table.Lookup("GET", "/connector_paystack/status")
	if result != RouteNotFound {
		t.Fatalf("result = %v, want RouteNotFound", result)
	}
}

func TestRegisterModuleRoutes_MissingLeadingSlash(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: "orders"},
	})
	if err == nil {
		t.Fatal("want error for a declared path missing its leading slash")
	}
}

func TestRegisterModuleRoutes_EmptyPath(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: ""},
	})
	if err == nil {
		t.Fatal("want error for an empty declared path")
	}
}

func TestRegisterModuleRoutes_RejectsDotDotEscape(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/../orders/hack"},
	})
	if err == nil {
		t.Fatal("want error for a declared path containing a \"..\" segment")
	}

	_, _, result, _ := table.Lookup("GET", "/orders/hack")
	if result != RouteNotFound {
		t.Fatalf("result = %v, want RouteNotFound — the escape must not have registered", result)
	}
}

func TestRegisterModuleRoutes_RejectsDotEscape(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/./orders"},
	})
	if err == nil {
		t.Fatal("want error for a declared path containing a \".\" segment")
	}
}

func TestRegisterModuleRoutes_RootPathDoesNotPanicReservedCheck(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/"},
	})
	if err != nil {
		t.Fatalf("RegisterModuleRoutes: %v", err)
	}
}

func TestRegisterModuleRoutes_ReservedUnderscorePrefix(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "_internal", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/"},
	})
	if err == nil {
		t.Fatal("want error: expanded path's first segment starts with \"_\"")
	}
}

func TestRegisterModuleRoutes_ReservedAuth(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "auth", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/"},
	})
	if err == nil {
		t.Fatal("want error: expanded path's first segment is \"auth\"")
	}
}

func TestRegisterModuleRoutes_ReservedAdmin(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "admin", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/"},
	})
	if err == nil {
		t.Fatal("want error: expanded path's first segment is \"admin\"")
	}
}

func TestRegisterModuleRoutes_ReservedConnectorsForNonConnector(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "connectors", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/foo"},
	})
	if err == nil {
		t.Fatal("want error: a non-connector module can't land its first segment on \"connectors\"")
	}
}

func TestRegisterModuleRoutes_FalsePositiveGuard(t *testing.T) {
	// "administration" must not be caught by a naive HasPrefix(path, "/admin") check.
	table := New()
	err := RegisterModuleRoutes(table, "administration", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/"},
	})
	if err != nil {
		t.Fatalf("RegisterModuleRoutes: %v, want no error for a module merely named similarly to a reserved segment", err)
	}

	_, _, result, _ := table.Lookup("GET", "/administration")
	if result != RouteFound {
		t.Fatalf("result = %v, want RouteFound", result)
	}
}

func TestRegisterModuleRoutes_TextualSelfDuplicate(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/{id}"},
		{Method: "GET", Path: "/{id}"},
	})
	if err == nil {
		t.Fatal("want error for a module declaring the same route twice")
	}

	_, _, result, _ := table.Lookup("GET", "/contacts/123")
	if result != RouteNotFound {
		t.Fatalf("result = %v, want RouteNotFound — a partially-failed batch must register nothing", result)
	}
}

func TestRegisterModuleRoutes_StructuralSelfDuplicate(t *testing.T) {
	// Different param names at the same tree position are still a collision.
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/{id}"},
		{Method: "GET", Path: "/{name}"},
	})
	if err == nil {
		t.Fatal("want error for two routes occupying the same tree position under different param names")
	}
}

func TestRegisterModuleRoutes_PartialBatchFailureLeavesTableUntouched(t *testing.T) {
	table := New()
	err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{
		{Method: "GET", Path: "/"},
		{Method: "GET", Path: "/{id}"},
		{Method: "GET", Path: "/../escape"},
	})
	if err == nil {
		t.Fatal("want error from the third route")
	}

	if _, _, result, _ := table.Lookup("GET", "/contacts"); result != RouteNotFound {
		t.Fatalf("result = %v, want RouteNotFound — the first good route must not have been registered either", result)
	}
	if _, _, result, _ := table.Lookup("GET", "/contacts/123"); result != RouteNotFound {
		t.Fatalf("result = %v, want RouteNotFound — the second good route must not have been registered either", result)
	}
}

func TestRegisterModuleRoutes_DifferentModulesSameShapeBothSucceed(t *testing.T) {
	table := New()
	if err := RegisterModuleRoutes(table, "contacts", "domain", []ExplicitRoute{{Method: "GET", Path: "/{id}"}}); err != nil {
		t.Fatalf("RegisterModuleRoutes(contacts): %v", err)
	}
	if err := RegisterModuleRoutes(table, "orders", "domain", []ExplicitRoute{{Method: "GET", Path: "/{id}"}}); err != nil {
		t.Fatalf("RegisterModuleRoutes(orders): %v", err)
	}

	if _, _, result, _ := table.Lookup("GET", "/contacts/1"); result != RouteFound {
		t.Fatalf("contacts lookup result = %v, want RouteFound", result)
	}
	if _, _, result, _ := table.Lookup("GET", "/orders/1"); result != RouteFound {
		t.Fatalf("orders lookup result = %v, want RouteFound", result)
	}
}

// Two sources sharing a module name must not silently clobber the table.
func TestRegisterModuleRoutes_CrossCallCollisionErrorsWithoutOverwriting(t *testing.T) {
	table := New()
	if err := RegisterModuleRoutes(table, "widgets", "domain", []ExplicitRoute{{Method: "GET", Path: "/"}}); err != nil {
		t.Fatalf("first RegisterModuleRoutes: %v", err)
	}

	err := RegisterModuleRoutes(table, "widgets", "domain", []ExplicitRoute{{Method: "GET", Path: "/"}})
	if err == nil {
		t.Fatal("want an error from the second call claiming an already-registered path, got nil")
	}

	if _, _, result, _ := table.Lookup("GET", "/widgets"); result != RouteFound {
		t.Fatalf("result = %v, want RouteFound — the first call's registration must still be intact", result)
	}
}
