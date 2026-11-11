package homesku

import (
	"testing"
)

func TestDetector_Constants(t *testing.T) {
	if ProGVLKKey == "" {
		t.Fatalf("ProGVLKKey must not be empty")
	}

	d := NewDetector()
	if d == nil {
		t.Fatalf("NewDetector returned nil")
	}
}

