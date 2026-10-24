// Package status combines log freshness and connection state into one outcome.
//
// A definite failure wins over an unknown signal. A TCP device is healthy only
// when its log was written recently and an established session is present.
// UDP has no session; recent log activity is the whole signal.
package status

// Vocabulary shared by the activity check, the session check, and the dashboard.
const (
	Healthy   = "healthy"
	Attention = "attention"
	Unknown   = "unknown"

	LogRecent  = "recent"
	LogStale   = "stale"
	LogMissing = "missing"
	LogEmpty   = "empty"
	LogUnknown = "unknown"

	ConnUDP          = "udp"
	ConnConnected    = "connected"
	ConnDisconnected = "disconnected"
	ConnUnknown      = "unknown"
)

// Combine returns healthy, attention, or unknown.
func Combine(logStatus, connection string) string {
	logProblem := logStatus == LogMissing || logStatus == LogEmpty || logStatus == LogStale
	if logProblem || connection == ConnDisconnected {
		return Attention
	}
	if logStatus != LogRecent || (connection != ConnUDP && connection != ConnConnected) {
		return Unknown
	}
	return Healthy
}
