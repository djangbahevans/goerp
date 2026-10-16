package metric

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRemovalRejectsLateObservations(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg)
	borrow := m.BorrowHistogram("sales")
	observe := func() {
		borrow.Observe(0.01)
		m.BorrowTimeout("sales")
		m.Compilation("sales", time.Second)
		m.Host("sales", "host.time.now", "success", time.Millisecond)
		m.Query("sales", "select", time.Millisecond)
		m.Job("sales", "invoice", "success", time.Second)
		m.Sync("sales", time.Second)
		m.SchemaOperation("sales", "add_table")
	}
	observe()

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				observe()
			}
		})
	}
	m.Remove("sales")
	wg.Wait()
	m.BorrowHistogram("sales").Observe(0.01)
	observe()
	if err := testutil.GatherAndCompare(reg, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}

	m.Activate("sales")
	m.Query("sales", "select", time.Millisecond)
	if count, err := testutil.GatherAndCount(reg, "erp_db_query_duration_seconds"); err != nil || count != 1 {
		t.Fatalf("reinstalled module query series = %d, error %v", count, err)
	}
}

func TestQueryOperationVocabulary(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg)
	for _, operation := range []string{"select", "insert", "update", "delete", "SELECT * FROM invoices", "invoice-123", ""} {
		m.Query("sales", operation, time.Second)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 1 || len(families[0].Metric) != 4 {
		t.Fatalf("unexpected query families: %v", families)
	}
	for _, sample := range families[0].Metric {
		if sample.GetHistogram().GetSampleCount() != 1 {
			t.Fatalf("unexpected query observation: %v", sample)
		}
	}
}
