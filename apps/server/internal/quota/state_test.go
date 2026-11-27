package quota

import "testing"

func TestTransitionQuotaState(t *testing.T) {
	tests := []struct {
		name    string
		current QuotaState
		usedPct float64
		want    QuotaState
	}{
		{"ok stays ok below 80", QuotaStateOK, 50.0, QuotaStateOK},
		{"ok crosses into warning80", QuotaStateOK, 85.0, QuotaStateWarning80},
		{"warning80 holds in band (hysteresis)", QuotaStateWarning80, 90.0, QuotaStateWarning80},
		{"warning80 escalates to critical95", QuotaStateWarning80, 96.0, QuotaStateCritical95},
		{"critical95 de-escalates to warning80", QuotaStateCritical95, 90.0, QuotaStateWarning80},
		{"warning80 drops to ok below 80", QuotaStateWarning80, 50.0, QuotaStateOK},
		{"critical95 drops to ok below 80", QuotaStateCritical95, 50.0, QuotaStateOK},
		{"exact 80 enters warning80", QuotaStateOK, 80.0, QuotaStateWarning80},
		{"exact 95 enters critical95", QuotaStateWarning80, 95.0, QuotaStateCritical95},
		{"over 100 is critical95", QuotaStateOK, 150.0, QuotaStateCritical95},
		{"ok holds in band when current was ok (no flap-up from below)", QuotaStateOK, 79.99, QuotaStateOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := transitionQuotaState(tc.current, tc.usedPct)
			if got != tc.want {
				t.Errorf("transitionQuotaState(%q, %.2f) = %q, want %q", tc.current, tc.usedPct, got, tc.want)
			}
		})
	}
}
