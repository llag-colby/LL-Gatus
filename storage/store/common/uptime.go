package common

// UptimeBucket is one stored uptime aggregate.
//
// Buckets are not guaranteed to all have the same width: uptime entries are stored hourly, but entries older than
// roughly 48 hours are merged into a single entry per day, which means that a range spanning more than 48 hours
// returns daily buckets for the older part of the range and hourly buckets for the most recent part.
// Callers that care about the width of a bucket must derive it from Timestamp, which is always the start of the
// bucket floored at the hour for hourly buckets and at the day for daily buckets.
type UptimeBucket struct {
	Timestamp            int64 // unix seconds, the bucket's start
	TotalExecutions      int
	SuccessfulExecutions int
	TotalResponseTime    int // milliseconds, summed across executions
}
