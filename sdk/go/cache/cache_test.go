package cache

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

type entry struct {
	value []byte
	ttl   int64
}

// fakeHost is an in-memory host.cache. getOrSet runs the loader through the
// real DispatchLoader memory path, as the engine does on a miss.
type fakeHost struct {
	entries     map[string]entry
	loaderCalls int
}

func installFakeHost(t *testing.T) *fakeHost {
	t.Helper()

	savedHost, savedNames, savedLoaders := host, names, loaders
	names, loaders = map[string]struct{}{}, map[string]func([]byte) ([]byte, error){}
	t.Cleanup(func() { host, names, loaders = savedHost, savedNames, savedLoaders })

	f := &fakeHost{entries: map[string]entry{}}
	host = hostCalls{
		get: func(in abi.CacheGetInput) (abi.CacheGetOutput, error) {
			e, ok := f.entries[in.Key]
			return abi.CacheGetOutput{Value: e.value, Found: ok}, nil
		},
		set: func(in abi.CacheSetInput) error {
			f.entries[in.Key] = entry{value: in.Value, ttl: in.TTLSeconds}
			return nil
		},
		delete: func(in abi.CacheDeleteInput) error {
			delete(f.entries, in.Key)
			return nil
		},
		invalidatePrefix: func(in abi.CacheInvalidatePrefixInput) error {
			for k := range f.entries {
				if strings.HasPrefix(k, in.Prefix) {
					delete(f.entries, k)
				}
			}
			return nil
		},
		getOrSet: func(in abi.CacheGetOrSetInput) (abi.CacheGetOrSetOutput, error) {
			if e, ok := f.entries[in.Key]; ok {
				return abi.CacheGetOrSetOutput{Value: e.value}, nil
			}
			f.loaderCalls++
			resp := dispatch(t, abi.CacheLoaderRequest{LoaderFnName: in.LoaderFnName, LoaderArgs: in.LoaderArgs})
			if resp.Error != nil {
				return abi.CacheGetOrSetOutput{}, &abi.HostError{Code: resp.Error.Code, Message: resp.Error.Message}
			}
			f.entries[in.Key] = entry{value: resp.Value, ttl: in.TTLSeconds}
			return abi.CacheGetOrSetOutput{Value: resp.Value}, nil
		},
	}
	return f
}

func dispatch(t *testing.T, req abi.CacheLoaderRequest) abi.CacheLoaderResponse {
	t.Helper()
	data, err := msgpack.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)

	packed := DispatchLoader(ptr, uint32(len(data)))

	var resp abi.CacheLoaderResponse
	if err := msgpack.Unmarshal(engine.ReadMem(uint32(packed>>32), uint32(packed)), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

type contact struct {
	ID   string `msgpack:"id"`
	Name string `msgpack:"name"`
}

type contactArgs struct {
	ID string `msgpack:"id"`
}

func contactKey(a contactArgs) string { return a.ID }

func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		got := recover()
		if got == nil {
			t.Fatalf("no panic, want one containing %q", want)
		}
		if msg, _ := got.(string); !strings.Contains(msg, want) {
			t.Fatalf("panic = %v, want it to contain %q", got, want)
		}
	}()
	fn()
}

func TestSetLookup_RoundTripsUnderNamespacedKey(t *testing.T) {
	f := installFakeHost(t)
	c := Define[contactArgs, contact]("contact", contactKey, TTL(5*time.Minute))

	if err := c.Set(contactArgs{ID: "c1"}, contact{ID: "c1", Name: "Kofi"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if got := slices.Collect(maps.Keys(f.entries)); !slices.Equal(got, []string{"contact:c1"}) {
		t.Errorf("stored keys = %v, want [contact:c1]", got)
	}
	if f.entries["contact:c1"].ttl != 300 {
		t.Errorf("ttl = %d, want the 5m default (300s)", f.entries["contact:c1"].ttl)
	}

	got, found, err := c.Lookup(contactArgs{ID: "c1"})
	if err != nil || !found || got != (contact{ID: "c1", Name: "Kofi"}) {
		t.Errorf("Lookup = %+v, %v, %v; want the stored contact", got, found, err)
	}
	if _, found, err := c.Lookup(contactArgs{ID: "absent"}); err != nil || found {
		t.Errorf("Lookup(absent) = found %v, err %v; want a clean miss", found, err)
	}
}

func TestSet_WithTTLOverridesDefault(t *testing.T) {
	f := installFakeHost(t)
	c := Define[contactArgs, contact]("contact", contactKey, TTL(5*time.Minute))

	if err := c.Set(contactArgs{ID: "c1"}, contact{}, WithTTL(90*time.Second)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := f.entries["contact:c1"].ttl; got != 90 {
		t.Errorf("ttl = %d, want 90", got)
	}
}

func TestSet_WithTTLUnderOneSecondIsRejected(t *testing.T) {
	f := installFakeHost(t)
	c := Define[contactArgs, contact]("contact", contactKey, TTL(time.Minute))

	for _, ttl := range []time.Duration{0, -time.Second, 500 * time.Millisecond} {
		if err := c.Set(contactArgs{ID: "c1"}, contact{}, WithTTL(ttl)); err == nil {
			t.Errorf("Set with WithTTL(%s) succeeded, want an error", ttl)
		}
	}
	if len(f.entries) != 0 {
		t.Errorf("a rejected Set stored %v", f.entries)
	}
}

func TestInvalidate_RemovesOneEntry(t *testing.T) {
	f := installFakeHost(t)
	c := Define[contactArgs, contact]("contact", contactKey, TTL(time.Minute))
	_ = c.Set(contactArgs{ID: "c1"}, contact{})
	_ = c.Set(contactArgs{ID: "c2"}, contact{})

	if err := c.Invalidate(contactArgs{ID: "c1"}); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, ok := f.entries["contact:c1"]; ok {
		t.Error("invalidated entry survived")
	}
	if _, ok := f.entries["contact:c2"]; !ok {
		t.Error("an unrelated entry was removed")
	}
}

func TestInvalidateAll_RemovesOnlyThisCache(t *testing.T) {
	f := installFakeHost(t)
	detail := Define[contactArgs, contact]("contact", contactKey, TTL(time.Minute))
	// A name sharing a leading substring must not match "contact:".
	list := Define[contactArgs, []contact]("contact_list", contactKey, TTL(time.Minute))
	_ = detail.Set(contactArgs{ID: "c1"}, contact{})
	_ = detail.Set(contactArgs{ID: "c2"}, contact{})
	_ = list.Set(contactArgs{ID: "page1"}, nil)

	if err := detail.InvalidateAll(); err != nil {
		t.Fatalf("InvalidateAll: %v", err)
	}
	if got := slices.Collect(maps.Keys(f.entries)); !slices.Equal(got, []string{"contact_list:page1"}) {
		t.Errorf("remaining keys = %v, want only the other cache's entry", got)
	}
}

func TestGet_MissRunsLoaderOnceAndStoresValue(t *testing.T) {
	f := installFakeHost(t)
	loads := 0
	c := Define[contactArgs, contact]("contact", contactKey, TTL(5*time.Minute)).Loader(func(a contactArgs) (contact, error) {
		loads++
		return contact{ID: a.ID, Name: "loaded"}, nil
	})

	for range 2 {
		got, err := c.Get(contactArgs{ID: "c1"})
		if err != nil || got != (contact{ID: "c1", Name: "loaded"}) {
			t.Fatalf("Get = %+v, %v; want the loaded contact", got, err)
		}
	}
	if loads != 1 {
		t.Errorf("loader ran %d times, want 1", loads)
	}
	if f.entries["contact:c1"].ttl != 300 {
		t.Errorf("stored ttl = %d, want 300", f.entries["contact:c1"].ttl)
	}

	got, found, err := c.Lookup(contactArgs{ID: "c1"})
	if err != nil || !found || got.Name != "loaded" {
		t.Errorf("Lookup after Get = %+v, %v, %v; want the stored value", got, found, err)
	}
}

func TestGet_LoaderErrorIsReturnedAndNothingStored(t *testing.T) {
	f := installFakeHost(t)
	c := Define[contactArgs, contact]("contact", contactKey, TTL(time.Minute)).Loader(func(contactArgs) (contact, error) {
		return contact{}, errors.New("boom")
	})

	_, err := c.Get(contactArgs{ID: "c1"})
	hostErr, ok := errors.AsType[*abi.HostError](err)
	if !ok || hostErr.Code != errCodeLoaderFailed || hostErr.Message != "boom" {
		t.Fatalf("Get error = %v, want a %s HostError carrying the loader's message", err, errCodeLoaderFailed)
	}
	if len(f.entries) != 0 {
		t.Errorf("a failed load stored %v", f.entries)
	}
}

func TestGet_LoaderHostErrorKeepsItsCode(t *testing.T) {
	installFakeHost(t)
	c := Define[contactArgs, contact]("contact", contactKey, TTL(time.Minute)).Loader(func(contactArgs) (contact, error) {
		return contact{}, &abi.HostError{Code: abi.ErrCodeNotFound, Message: "no such contact"}
	})

	_, err := c.Get(contactArgs{ID: "c1"})
	if hostErr, ok := errors.AsType[*abi.HostError](err); !ok || hostErr.Code != abi.ErrCodeNotFound {
		t.Errorf("Get error = %v, want code %s", err, abi.ErrCodeNotFound)
	}
}

func TestDispatchLoader_UnknownCacheIsAnError(t *testing.T) {
	installFakeHost(t)

	resp := dispatch(t, abi.CacheLoaderRequest{LoaderFnName: "nope"})
	if resp.Error == nil || resp.Error.Code != errCodeLoaderNotRegistered {
		t.Errorf("response = %+v, want %s", resp, errCodeLoaderNotRegistered)
	}
}

func TestLookup_UndecodableEntryIsAnError(t *testing.T) {
	f := installFakeHost(t)
	c := Define[contactArgs, contact]("contact", contactKey, TTL(time.Minute))
	f.entries["contact:c1"] = entry{value: []byte{0xc1}}

	if _, found, err := c.Lookup(contactArgs{ID: "c1"}); err == nil || found {
		t.Errorf("Lookup = found %v, err %v; want a decode error", found, err)
	}
}

func TestDefine_RejectsInvalidDefinitions(t *testing.T) {
	installFakeHost(t)

	mustPanic(t, "no ':'", func() { Define[contactArgs, contact]("", contactKey, TTL(time.Minute)) })
	mustPanic(t, "no ':'", func() { Define[contactArgs, contact]("a:b", contactKey, TTL(time.Minute)) })
	mustPanic(t, "key function", func() { Define[contactArgs, contact]("nokey", nil, TTL(time.Minute)) })
	mustPanic(t, "cache.TTL", func() { Define[contactArgs, contact]("nottl", contactKey) })
	mustPanic(t, "cache.TTL", func() { Define[contactArgs, contact]("shortttl", contactKey, TTL(500*time.Millisecond)) })
}

func TestDefine_DuplicateNameFailsAtRegistration(t *testing.T) {
	installFakeHost(t)
	Define[contactArgs, contact]("contact", contactKey, TTL(time.Minute))

	mustPanic(t, "already defined", func() {
		Define[contactArgs, []contact]("contact", contactKey, TTL(time.Minute))
	})
}

func TestLoader_SecondLoaderOnOneCachePanics(t *testing.T) {
	installFakeHost(t)
	c := Define[contactArgs, contact]("contact", contactKey, TTL(time.Minute))
	load := func(contactArgs) (contact, error) { return contact{}, nil }
	c.Loader(load)

	mustPanic(t, "already has a loader", func() { c.Loader(load) })
}
