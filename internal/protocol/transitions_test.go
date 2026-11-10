package protocol

import "testing"

func TestIsValidClientTransition(t *testing.T) {
	tests := []struct {
		name string
		from ClientStatus
		to   ClientStatus
		want bool
	}{
		// Valid transitions
		{"OFFLINE to WAKING_UP", ClientStatusOffline, ClientStatusWakingUp, true},
		{"OFFLINE to READY_FOR_CAST", ClientStatusOffline, ClientStatusReadyForCast, true},
		{"WAKING_UP to ONLINE_WINDOWS", ClientStatusWakingUp, ClientStatusOnlineWindows, true},
		{"WAKING_UP to OFFLINE", ClientStatusWakingUp, ClientStatusOffline, true},
		{"ONLINE_WINDOWS to ENTERING_PE", ClientStatusOnlineWindows, ClientStatusEnteringPE, true},
		{"ONLINE_WINDOWS to OFFLINE", ClientStatusOnlineWindows, ClientStatusOffline, true},
		{"ENTERING_PE to READY_FOR_CAST", ClientStatusEnteringPE, ClientStatusReadyForCast, true},
		{"ENTERING_PE to ERROR", ClientStatusEnteringPE, ClientStatusError, true},
		{"READY_FOR_CAST to DEPLOYING", ClientStatusReadyForCast, ClientStatusDeploying, true},
		{"READY_FOR_CAST to ONLINE_WINDOWS", ClientStatusReadyForCast, ClientStatusOnlineWindows, true},
		{"DEPLOYING to VERIFYING", ClientStatusDeploying, ClientStatusVerifying, true},
		{"VERIFYING to POST_RESTORING_NET", ClientStatusVerifying, ClientStatusPostRestoringNet, true},
		{"VERIFYING to ERROR", ClientStatusVerifying, ClientStatusError, true},
		{"POST_RESTORING_NET to ONLINE_WINDOWS", ClientStatusPostRestoringNet, ClientStatusOnlineWindows, true},
		{"Any state to ERROR (from DEPLOYING)", ClientStatusDeploying, ClientStatusError, true},
		{"Any state to ERROR (from ONLINE_WINDOWS)", ClientStatusOnlineWindows, ClientStatusError, true},
		{"ERROR to OFFLINE", ClientStatusError, ClientStatusOffline, true},
		{"ERROR to ONLINE_WINDOWS", ClientStatusError, ClientStatusOnlineWindows, true},

		// Invalid transitions
		{"OFFLINE to DEPLOYING", ClientStatusOffline, ClientStatusDeploying, false},
		{"OFFLINE to VERIFYING", ClientStatusOffline, ClientStatusVerifying, false},
		{"WAKING_UP to DEPLOYING", ClientStatusWakingUp, ClientStatusDeploying, false},
		{"ONLINE_WINDOWS to DEPLOYING", ClientStatusOnlineWindows, ClientStatusDeploying, false},
		{"ENTERING_PE to ONLINE_WINDOWS", ClientStatusEnteringPE, ClientStatusOnlineWindows, false},
		{"DEPLOYING to ONLINE_WINDOWS", ClientStatusDeploying, ClientStatusOnlineWindows, false},
		{"DEPLOYING to READY_FOR_CAST", ClientStatusDeploying, ClientStatusReadyForCast, false},
		{"VERIFYING to DEPLOYING", ClientStatusVerifying, ClientStatusDeploying, false},
		{"POST_RESTORING_NET to DEPLOYING", ClientStatusPostRestoringNet, ClientStatusDeploying, false},
		{"ERROR to DEPLOYING", ClientStatusError, ClientStatusDeploying, false},
		{"Invalid unknown state", "UNKNOWN", ClientStatusOnlineWindows, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsValidClientTransition(tc.from, tc.to)
			if got != tc.want {
				t.Errorf("IsValidClientTransition(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestIsValidTaskTransition(t *testing.T) {
	tests := []struct {
		name string
		from TaskState
		to   TaskState
		want bool
	}{
		// Valid transitions
		{"PENDING_PREPARE to PREPARING", TaskStatePendingPrepare, TaskStatePreparing, true},
		{"PENDING_PREPARE to CANCELLED", TaskStatePendingPrepare, TaskStateCancelled, true},
		{"PREPARING to READY", TaskStatePreparing, TaskStateReady, true},
		{"PREPARING to FAILED", TaskStatePreparing, TaskStateFailed, true},
		{"PREPARING to CANCELLED", TaskStatePreparing, TaskStateCancelled, true},
		{"READY to CASTING", TaskStateReady, TaskStateCasting, true},
		{"READY to CANCELLED", TaskStateReady, TaskStateCancelled, true},
		{"CASTING to VERIFYING", TaskStateCasting, TaskStateVerifying, true},
		{"CASTING to FAILED", TaskStateCasting, TaskStateFailed, true},
		{"VERIFYING to POST_RESTORE", TaskStateVerifying, TaskStatePostRestore, true},
		{"VERIFYING to FAILED", TaskStateVerifying, TaskStateFailed, true},
		{"POST_RESTORE to SUCCEEDED", TaskStatePostRestore, TaskStateSucceeded, true},
		{"POST_RESTORE to FAILED", TaskStatePostRestore, TaskStateFailed, true},

		// Terminal states cannot transition
		{"SUCCEEDED to CASTING", TaskStateSucceeded, TaskStateCasting, false},
		{"FAILED to CASTING", TaskStateFailed, TaskStateCasting, false},
		{"CANCELLED to PREPARING", TaskStateCancelled, TaskStatePreparing, false},

		// Invalid transitions
		{"PENDING_PREPARE to CASTING", TaskStatePendingPrepare, TaskStateCasting, false},
		{"PREPARING to CASTING", TaskStatePreparing, TaskStateCasting, false},
		{"READY to POST_RESTORE", TaskStateReady, TaskStatePostRestore, false},
		{"CASTING to SUCCEEDED", TaskStateCasting, TaskStateSucceeded, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsValidTaskTransition(tc.from, tc.to)
			if got != tc.want {
				t.Errorf("IsValidTaskTransition(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}
