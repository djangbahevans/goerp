package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func openTestCacheClient(t *testing.T) *cache.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	c, err := cache.New(ctx, cache.Config{Addr: "localhost:6379", DB: 0, MaxRetries: 1})
	if err != nil {
		t.Skipf("redis not reachable at localhost:6379 (start compose.dev.yml): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// newHostORMTransientTestRuntime is newHostDBTestRuntime plus a real
// cache.Client — the Table-backed tests don't need Redis at all, but
// Transient routing (host_orm_transient.go) is unreachable without one.
// primaryDB is still required (registerHostDB/registerHostORM's Table
// path close over it unconditionally), even though no test in this file
// issues a single SQL query.
func newHostORMTransientTestRuntime(t *testing.T, primaryDB *sql.DB, cacheClient *cache.Client) *Runtime {
	t.Helper()
	rt, err := New(&config.Config{
		CompilationCache:  t.TempDir(),
		Environment:       string(config.Production),
		PoolMaxMemoryByes: 1 << 20,
	}, primaryDB, nil, cacheClient)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	return rt
}

func transientItemModelDecl(ttl time.Duration) model.ModelDeclaration {
	d := model.Define("wizard_item", model.Transient(ttl)).
		Field("id", model.UUID().PrimaryKey()).
		Field("name", model.Text().Required()).
		Field("etag", model.Text())
	return *d
}

func newTransientTestModuleContext(tenantSlug string, modelDecls []model.ModelDeclaration) *ModuleContext {
	return NewModuleContext("req-1", "testmodule", "user-1", "contact-1", []string{"admin"}, nil, tenantSlug, tenantSlug, "trace-1",
		abi.CapDBRead|abi.CapDBWrite, nil, ModuleSnapshot{ModelDecls: modelDecls})
}

func TestHostORM_Transient_CreateThenRead_RoundTrips(t *testing.T) {
	ctx := context.Background()
	cacheClient := openTestCacheClient(t)
	primaryDB := openTestPrimaryDB(t)
	rt := newHostORMTransientTestRuntime(t, primaryDB, cacheClient)

	slug := fmt.Sprintf("transienttest%d", time.Now().UnixNano())
	md := transientItemModelDecl(time.Minute)
	mc := newTransientTestModuleContext(slug, []model.ModelDeclaration{md})

	writeInst := newHostORMWriteCaller(t, ctx, rt, mc)
	readInst := newHostORMCaller(t, ctx, rt, mc)

	var created ORMCreateOutput
	env := callORMHost(t, ctx, writeInst, "call_create", ORMCreateInput{
		Model:  "testmodule.wizard_item",
		Record: map[string]any{"name": "Step 1"},
	}, &created)
	if !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id, _ := created.Record["id"].(string)
	if id == "" {
		t.Fatal("expected create to assign an id")
	}
	t.Cleanup(func() { _ = cacheClient.Delete(context.Background(), transientKey(slug, "testmodule.wizard_item", id)) })

	var read ORMReadOutput
	env = callORMHost(t, ctx, readInst, "call_read", ORMReadInput{Model: "testmodule.wizard_item", IDs: []string{id}}, &read)
	if !env.OK {
		t.Fatalf("read failed: %+v", env.Error)
	}
	if len(read.Records) != 1 || read.Records[0]["name"] != "Step 1" {
		t.Errorf("Records = %+v, want one record with name=Step 1", read.Records)
	}
}

func TestHostORM_Transient_Write_CorrectEtag_SucceedsAndRotatesEtag(t *testing.T) {
	ctx := context.Background()
	cacheClient := openTestCacheClient(t)
	primaryDB := openTestPrimaryDB(t)
	rt := newHostORMTransientTestRuntime(t, primaryDB, cacheClient)

	slug := fmt.Sprintf("transientwritetest%d", time.Now().UnixNano())
	md := transientItemModelDecl(time.Minute)
	mc := newTransientTestModuleContext(slug, []model.ModelDeclaration{md})

	writeInst := newHostORMWriteCaller(t, ctx, rt, mc)

	var created ORMCreateOutput
	if env := callORMHost(t, ctx, writeInst, "call_create", ORMCreateInput{
		Model: "testmodule.wizard_item", Record: map[string]any{"name": "Step 1"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id := created.Record["id"].(string)
	originalEtag := created.Record["etag"].(string)
	t.Cleanup(func() { _ = cacheClient.Delete(context.Background(), transientKey(slug, "testmodule.wizard_item", id)) })

	var written ORMWriteOutput
	env := callORMHost(t, ctx, writeInst, "call_write", ORMWriteInput{
		Model: "testmodule.wizard_item", ID: id, Record: map[string]any{"name": "Step 2"}, ExpectedEtag: originalEtag,
	}, &written)
	if !env.OK {
		t.Fatalf("write failed: %+v", env.Error)
	}
	if written.Record["name"] != "Step 2" {
		t.Errorf("Record[name] = %v, want Step 2", written.Record["name"])
	}
	if written.Record["etag"] == originalEtag {
		t.Error("expected etag to rotate on a successful write")
	}
}

func TestHostORM_Transient_Write_StaleEtag_EtagMismatch(t *testing.T) {
	ctx := context.Background()
	cacheClient := openTestCacheClient(t)
	primaryDB := openTestPrimaryDB(t)
	rt := newHostORMTransientTestRuntime(t, primaryDB, cacheClient)

	slug := fmt.Sprintf("transientstaletest%d", time.Now().UnixNano())
	md := transientItemModelDecl(time.Minute)
	mc := newTransientTestModuleContext(slug, []model.ModelDeclaration{md})

	writeInst := newHostORMWriteCaller(t, ctx, rt, mc)

	var created ORMCreateOutput
	if env := callORMHost(t, ctx, writeInst, "call_create", ORMCreateInput{
		Model: "testmodule.wizard_item", Record: map[string]any{"name": "Step 1"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id := created.Record["id"].(string)
	t.Cleanup(func() { _ = cacheClient.Delete(context.Background(), transientKey(slug, "testmodule.wizard_item", id)) })

	env := callORMHost(t, ctx, writeInst, "call_write", ORMWriteInput{
		Model: "testmodule.wizard_item", ID: id, Record: map[string]any{"name": "Step 2"}, ExpectedEtag: "stale-etag",
	}, nil)
	if env.OK {
		t.Fatal("expected a stale etag to fail")
	}
	if env.Error.Code != abi.ErrCodeEtagMismatch {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeEtagMismatch)
	}
}

func TestHostORM_Transient_ReadMissingKey_NotFound(t *testing.T) {
	ctx := context.Background()
	cacheClient := openTestCacheClient(t)
	primaryDB := openTestPrimaryDB(t)
	rt := newHostORMTransientTestRuntime(t, primaryDB, cacheClient)

	slug := fmt.Sprintf("transientmissingtest%d", time.Now().UnixNano())
	md := transientItemModelDecl(time.Minute)
	mc := newTransientTestModuleContext(slug, []model.ModelDeclaration{md})
	readInst := newHostORMCaller(t, ctx, rt, mc)

	env := callORMHost(t, ctx, readInst, "call_read", ORMReadInput{
		Model: "testmodule.wizard_item", IDs: []string{"99999999-9999-9999-9999-999999999999"},
	}, nil)
	if env.OK {
		t.Fatal("expected read of a missing key to fail")
	}
	if env.Error.Code != abi.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeNotFound)
	}
}

func TestHostORM_Transient_ExpiredKey_NotFound(t *testing.T) {
	ctx := context.Background()
	cacheClient := openTestCacheClient(t)
	primaryDB := openTestPrimaryDB(t)
	rt := newHostORMTransientTestRuntime(t, primaryDB, cacheClient)

	slug := fmt.Sprintf("transientexpiretest%d", time.Now().UnixNano())
	md := transientItemModelDecl(200 * time.Millisecond)
	mc := newTransientTestModuleContext(slug, []model.ModelDeclaration{md})
	writeInst := newHostORMWriteCaller(t, ctx, rt, mc)
	readInst := newHostORMCaller(t, ctx, rt, mc)

	var created ORMCreateOutput
	if env := callORMHost(t, ctx, writeInst, "call_create", ORMCreateInput{
		Model: "testmodule.wizard_item", Record: map[string]any{"name": "Step 1"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id := created.Record["id"].(string)

	time.Sleep(400 * time.Millisecond)

	env := callORMHost(t, ctx, readInst, "call_read", ORMReadInput{Model: "testmodule.wizard_item", IDs: []string{id}}, nil)
	if env.OK {
		t.Fatal("expected read of an expired key to fail")
	}
	if env.Error.Code != abi.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeNotFound)
	}
}

func TestHostORM_Transient_SearchAndSearchRead_NotListable(t *testing.T) {
	ctx := context.Background()
	cacheClient := openTestCacheClient(t)
	primaryDB := openTestPrimaryDB(t)
	rt := newHostORMTransientTestRuntime(t, primaryDB, cacheClient)

	slug := fmt.Sprintf("transientlisttest%d", time.Now().UnixNano())
	md := transientItemModelDecl(time.Minute)
	mc := newTransientTestModuleContext(slug, []model.ModelDeclaration{md})
	readInst := newHostORMCaller(t, ctx, rt, mc)

	env := callORMHost(t, ctx, readInst, "call_search", ORMSearchInput{Model: "testmodule.wizard_item"}, nil)
	if env.OK {
		t.Fatal("expected search on a Transient model to fail")
	}
	if env.Error.Code != abi.ErrCodeTransientNotListable {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeTransientNotListable)
	}

	env = callORMHost(t, ctx, readInst, "call_search_read", ORMSearchReadInput{Model: "testmodule.wizard_item"}, nil)
	if env.OK {
		t.Fatal("expected search_read on a Transient model to fail")
	}
	if env.Error.Code != abi.ErrCodeTransientNotListable {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeTransientNotListable)
	}
}

func TestHostORM_Transient_Unlink_RemovesKey(t *testing.T) {
	ctx := context.Background()
	cacheClient := openTestCacheClient(t)
	primaryDB := openTestPrimaryDB(t)
	rt := newHostORMTransientTestRuntime(t, primaryDB, cacheClient)

	slug := fmt.Sprintf("transientunlinktest%d", time.Now().UnixNano())
	md := transientItemModelDecl(time.Minute)
	mc := newTransientTestModuleContext(slug, []model.ModelDeclaration{md})
	writeInst := newHostORMWriteCaller(t, ctx, rt, mc)
	readInst := newHostORMCaller(t, ctx, rt, mc)

	var created ORMCreateOutput
	if env := callORMHost(t, ctx, writeInst, "call_create", ORMCreateInput{
		Model: "testmodule.wizard_item", Record: map[string]any{"name": "Step 1"},
	}, &created); !env.OK {
		t.Fatalf("create failed: %+v", env.Error)
	}
	id := created.Record["id"].(string)

	var out ORMUnlinkOutput
	env := callORMHost(t, ctx, writeInst, "call_unlink", ORMUnlinkInput{Model: "testmodule.wizard_item", ID: id}, &out)
	if !env.OK {
		t.Fatalf("unlink failed: %+v", env.Error)
	}
	if !out.Deleted {
		t.Error("expected Deleted = true")
	}

	env = callORMHost(t, ctx, readInst, "call_read", ORMReadInput{Model: "testmodule.wizard_item", IDs: []string{id}}, nil)
	if env.OK {
		t.Fatal("expected read after unlink to fail")
	}
	if env.Error.Code != abi.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeNotFound)
	}
}

func TestHostORM_Transient_TenantScoping_NoCrossTenantCollision(t *testing.T) {
	ctx := context.Background()
	cacheClient := openTestCacheClient(t)
	primaryDB := openTestPrimaryDB(t)
	rt := newHostORMTransientTestRuntime(t, primaryDB, cacheClient)

	md := transientItemModelDecl(time.Minute)
	tenantA := fmt.Sprintf("tenanta%d", time.Now().UnixNano())
	tenantB := fmt.Sprintf("tenantb%d", time.Now().UnixNano())

	mcA := newTransientTestModuleContext(tenantA, []model.ModelDeclaration{md})
	mcB := newTransientTestModuleContext(tenantB, []model.ModelDeclaration{md})
	writeInstA := newHostORMWriteCaller(t, ctx, rt, mcA)
	writeInstB := newHostORMWriteCaller(t, ctx, rt, mcB)
	readInstB := newHostORMCaller(t, ctx, rt, mcB)

	id := "11111111-1111-1111-1111-111111111111"
	if env := callORMHost(t, ctx, writeInstA, "call_create", ORMCreateInput{
		Model: "testmodule.wizard_item", Record: map[string]any{"id": id, "name": "Tenant A's data"},
	}, nil); !env.OK {
		t.Fatalf("tenant A create failed: %+v", env.Error)
	}
	t.Cleanup(func() {
		_ = cacheClient.Delete(context.Background(), transientKey(tenantA, "testmodule.wizard_item", id))
	})

	// Tenant B never created this ID — must read as not_found, not
	// tenant A's record.
	env := callORMHost(t, ctx, readInstB, "call_read", ORMReadInput{Model: "testmodule.wizard_item", IDs: []string{id}}, nil)
	if env.OK {
		t.Fatal("expected tenant B's read of tenant A's ID to fail")
	}
	if env.Error.Code != abi.ErrCodeNotFound {
		t.Errorf("Error.Code = %q, want %q", env.Error.Code, abi.ErrCodeNotFound)
	}

	// Tenant B can independently create the *same* ID with its own data.
	if env := callORMHost(t, ctx, writeInstB, "call_create", ORMCreateInput{
		Model: "testmodule.wizard_item", Record: map[string]any{"id": id, "name": "Tenant B's data"},
	}, nil); !env.OK {
		t.Fatalf("tenant B create failed: %+v", env.Error)
	}
	t.Cleanup(func() {
		_ = cacheClient.Delete(context.Background(), transientKey(tenantB, "testmodule.wizard_item", id))
	})

	var readA ORMReadOutput
	readInstA := newHostORMCaller(t, ctx, rt, mcA)
	if env := callORMHost(t, ctx, readInstA, "call_read", ORMReadInput{Model: "testmodule.wizard_item", IDs: []string{id}}, &readA); !env.OK {
		t.Fatalf("tenant A read failed: %+v", env.Error)
	}
	if readA.Records[0]["name"] != "Tenant A's data" {
		t.Errorf("tenant A's record = %+v, want name=Tenant A's data (not overwritten by tenant B)", readA.Records[0])
	}
}
