package jobqueue

import (
	"fmt"
	"strconv"
	"strings"
)

// JobIDPrefix is prepended to River's internal int64 job id to form the
// wire-format job id (job_<int>, cli-reference.md's "IDs are strings"
// convention) — not a synthetic ULID, just a prefix around River's own
// sequence value. EncodeJobID/DecodeJobID are the only place that
// translation happens; every admin API route and async operation that
// hands a job id back to a caller goes through one or the other.
const JobIDPrefix = "job_"

func EncodeJobID(id int64) string {
	return JobIDPrefix + strconv.FormatInt(id, 10)
}

func DecodeJobID(s string) (int64, error) {
	n, ok := strings.CutPrefix(s, JobIDPrefix)
	if !ok {
		return 0, fmt.Errorf("job id %q missing %q prefix", s, JobIDPrefix)
	}
	id, err := strconv.ParseInt(n, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("job id %q is not numeric: %w", s, err)
	}
	return id, nil
}
