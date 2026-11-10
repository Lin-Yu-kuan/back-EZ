package protocol

// IsValidClientTransition checks if the transition from -> to is allowed by SPEC §2.6.
func IsValidClientTransition(from, to ClientStatus) bool {
	// Any state can transition to ERROR (SPEC §2.6: "任一狀態 -> ERROR")
	if to == ClientStatusError {
		return true
	}

	switch from {
	case ClientStatusOffline:
		return to == ClientStatusWakingUp || to == ClientStatusReadyForCast

	case ClientStatusWakingUp:
		return to == ClientStatusOnlineWindows || to == ClientStatusOffline

	case ClientStatusOnlineWindows:
		return to == ClientStatusEnteringPE || to == ClientStatusOffline

	case ClientStatusEnteringPE:
		return to == ClientStatusReadyForCast || to == ClientStatusError

	case ClientStatusReadyForCast:
		return to == ClientStatusDeploying || to == ClientStatusOnlineWindows

	case ClientStatusDeploying:
		return to == ClientStatusVerifying

	case ClientStatusVerifying:
		return to == ClientStatusPostRestoringNet || to == ClientStatusError

	case ClientStatusPostRestoringNet:
		return to == ClientStatusOnlineWindows

	case ClientStatusError:
		return to == ClientStatusOffline || to == ClientStatusOnlineWindows

	default:
		return false
	}
}

// IsValidTaskTransition checks if the deploy task transition from -> to is allowed by SPEC §2.6.
func IsValidTaskTransition(from, to TaskState) bool {
	switch from {
	case TaskStatePendingPrepare:
		return to == TaskStatePreparing || to == TaskStateCancelled

	case TaskStatePreparing:
		return to == TaskStateReady || to == TaskStateFailed || to == TaskStateCancelled

	case TaskStateReady:
		return to == TaskStateCasting || to == TaskStateCancelled

	case TaskStateCasting:
		return to == TaskStateVerifying || to == TaskStateFailed

	case TaskStateVerifying:
		return to == TaskStatePostRestore || to == TaskStateFailed

	case TaskStatePostRestore:
		return to == TaskStateSucceeded || to == TaskStateFailed

	case TaskStateSucceeded, TaskStateFailed, TaskStateCancelled:
		// Terminal states cannot transition further
		return false

	default:
		return false
	}
}
