package abi

import "fmt"

type CapabilitySet uint64

const (
	CapDBRead CapabilitySet = 1 << iota
	CapDBWrite
	CapDBNotify
	CapCacheRead
	CapCacheWrite
	CapEventEmit
	CapStorageRead
	CapStorageWrite
	CapHTTPFetch
	CapJobsEnqueue
	CapNotifySend
	CapNotifyManageDeliveries
	CapWebhooksDeliver
	CapUIPush
	CapSearchQuery
	CapSearchIndex
	CapAuthzCheck
	CapWorkflowStart
	CapAnalyticsQuery
	CapAnalyticsInsert
	CapAnalyticsExport
	// CapDBMigrationDDL gates host.db.migration_ddl (host_db_migration_ddl.go,
	// goerp#500) — appended last rather than inserted among the db.* bits
	// above so every existing capability's bit value stays stable.
	CapDBMigrationDDL
)

func (cs CapabilitySet) Has(cap CapabilitySet) bool {
	return cs&cap != 0
}

var capabilityBits = map[string]CapabilitySet{
	"db.read":                  CapDBRead,
	"db.write":                 CapDBWrite,
	"db.notify":                CapDBNotify,
	"cache.read":               CapCacheRead,
	"cache.write":              CapCacheWrite,
	"event.emit":               CapEventEmit,
	"storage.read":             CapStorageRead,
	"storage.write":            CapStorageWrite,
	"http.fetch":               CapHTTPFetch,
	"jobs.enqueue":             CapJobsEnqueue,
	"notify.send":              CapNotifySend,
	"notify.manage_deliveries": CapNotifyManageDeliveries,
	"webhooks.deliver":         CapWebhooksDeliver,
	"ui.push":                  CapUIPush,
	"search.query":             CapSearchQuery,
	"search.index":             CapSearchIndex,
	"authz.check":              CapAuthzCheck,
	"workflow.start":           CapWorkflowStart,
	"analytics.query":          CapAnalyticsQuery,
	"analytics.insert":         CapAnalyticsInsert,
	"analytics.export":         CapAnalyticsExport,
	"db.migration_ddl":         CapDBMigrationDDL,
}

func ResolveCapabilities(caps []string) (CapabilitySet, error) {
	var cs CapabilitySet
	for _, c := range caps {
		bit, ok := capabilityBits[c]
		if !ok {
			return 0, fmt.Errorf("unknown capability %q", c)
		}
		cs |= bit
	}

	return cs, nil
}
