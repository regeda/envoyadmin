package snapshot

import (
	adminv3 "github.com/envoyproxy/go-control-plane/envoy/admin/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
)

var unhealthyFlags = map[string]bool{
	"FAILED_ACTIVE_HC":               true,
	"FAILED_OUTLIER_CHECK":           true,
	"EDS_STATUS_UNHEALTHY":           true,
	"EDS_STATUS_TIMEOUT":             true,
	"ACTIVE_HC_TIMEOUT":              true,
	"EXCLUDED_VIA_IMMEDIATE_HC_FAIL": true,
	"PENDING_DYNAMIC_REMOVAL":        true,
}

// IsUnhealthyFlag reports whether a flag means the host is not serving traffic.
// Degraded and pending flags are informational.
func IsUnhealthyFlag(flag string) bool { return unhealthyFlags[flag] }

func flagsOf(hs *adminv3.HostHealthStatus) []string {
	if hs == nil {
		return nil
	}
	var f []string
	add := func(on bool, name string) {
		if on {
			f = append(f, name)
		}
	}
	add(hs.GetFailedActiveHealthCheck(), "FAILED_ACTIVE_HC")
	add(hs.GetFailedOutlierCheck(), "FAILED_OUTLIER_CHECK")
	add(hs.GetFailedActiveDegradedCheck(), "DEGRADED_ACTIVE_HC")
	add(hs.GetPendingDynamicRemoval(), "PENDING_DYNAMIC_REMOVAL")
	add(hs.GetPendingActiveHc(), "PENDING_ACTIVE_HC")
	add(hs.GetExcludedViaImmediateHcFail(), "EXCLUDED_VIA_IMMEDIATE_HC_FAIL")
	add(hs.GetActiveHcTimeout(), "ACTIVE_HC_TIMEOUT")
	switch hs.GetEdsHealthStatus() {
	case corev3.HealthStatus_UNHEALTHY:
		f = append(f, "EDS_STATUS_UNHEALTHY")
	case corev3.HealthStatus_DEGRADED:
		f = append(f, "EDS_STATUS_DEGRADED")
	case corev3.HealthStatus_TIMEOUT:
		f = append(f, "EDS_STATUS_TIMEOUT")
	case corev3.HealthStatus_DRAINING:
		f = append(f, "EDS_STATUS_DRAINING")
	}
	return f
}
