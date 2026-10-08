package abi

// TimeNowOutput reports the authoritative wall-clock time in UTC.
type TimeNowOutput struct {
	UnixMs  int64  `msgpack:"unix_ms"`
	ISO8601 string `msgpack:"iso8601"`
}
