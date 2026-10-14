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
	"EDS_STATUS_DRAINING":            true,
	"ACTIVE_HC_TIMEOUT":              true,
	"EXCLUDED_VIA_IMMEDIATE_HC_FAIL": true,
	"PENDING_DYNAMIC_REMOVAL":        true,
}

// IsUnhealthyFlag reports whether a flag means the host is not getting traffic.
//
// It follows Envoy's HealthFlag semantics: FAILED_ACTIVE_HC, FAILED_OUTLIER_CHECK,
// EXCLUDED_VIA_IMMEDIATE_HC_FAIL and PENDING_DYNAMIC_REMOVAL (a host removed from
// discovery that is still draining on an active health check) take the host out
// of rotation. Envoy sets FAILED_EDS_HEALTH for EDS UNHEALTHY, DRAINING and
// TIMEOUT, so all three count. ACTIVE_HC_TIMEOUT accompanies a failed check.
// The DEGRADED flags (health check or EDS) only reduce a host's priority and it
// still serves traffic, and PENDING_ACTIVE_HC just means the first active health
// check has not completed yet, so those are informational.
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
