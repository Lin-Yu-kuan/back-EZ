package multicast_test

import (
	"context"
	"errors"
	"testing"

	"github.com/owner/back-ez/internal/protocol"
	"github.com/owner/back-ez/internal/workers/multicast"
)

func TestParseStatLine(t *testing.T) {
	tests := []struct {
		line        string
		wantPercent float64
		wantSpeed   float64
		wantOK      bool
	}{
		{
			line:        "bytes= 104857600 ( 25.50 %)  speed= 85.20 Mbps",
			wantPercent: 25.50,
			wantSpeed:   85.20,
			wantOK:      true,
		},
		{
			line:        "bytes= 524288000 ( 100.0 %)  speed= 112.50 MB/s",
			wantPercent: 100.0,
			wantSpeed:   112.50,
			wantOK:      true,
		},
		{
			line:        "Transferring...  45.0 %",
			wantPercent: 45.0,
			wantSpeed:   0,
			wantOK:      true,
		},
		{
			line:        "some random log without stats",
			wantPercent: 0,
			wantSpeed:   0,
			wantOK:      false,
		},
	}

	for _, tt := range tests {
		p, s, ok := multicast.ParseStatLine(tt.line)
		if ok != tt.wantOK {
			t.Errorf("line %q: got ok=%v, want %v", tt.line, ok, tt.wantOK)
		}
		if ok {
			if p != tt.wantPercent || s != tt.wantSpeed {
				t.Errorf("line %q: got (%.2f, %.2f), want (%.2f, %.2f)", tt.line, p, s, tt.wantPercent, tt.wantSpeed)
			}
		}
	}
}

func TestCoordinator_SpaceCheck(t *testing.T) {
	cfg := multicast.Config{
		DryRun: true,
		MockFreeSpaceFunc: func(path string) (uint64, error) {
			// Mock available space: 100 MB
			return 100 * 1024 * 1024, nil
		},
	}
	coord := multicast.NewCoordinator(cfg)

	// Case 1: Image size 100 MB -> Required 105 MB -> Insufficient!
	err := coord.CheckFreeSpace("C:\\EZRestore", 100*1024*1024)
	if err == nil {
		t.Fatalf("expected error for insufficient space, got nil")
	}
	if !errors.Is(err, multicast.ErrInsufficientSpace) {
		t.Fatalf("expected ErrInsufficientSpace, got %v", err)
	}

	// Case 2: Image size 50 MB -> Required 52.5 MB -> Sufficient!
	err = coord.CheckFreeSpace("C:\\EZRestore", 50*1024*1024)
	if err != nil {
		t.Fatalf("expected sufficient space, got %v", err)
	}
}

func TestCoordinator_DryRunFlowAndVerification(t *testing.T) {
	cfg := multicast.Config{
		DryRun: true,
		MockFreeSpaceFunc: func(path string) (uint64, error) {
			return 10 * 1024 * 1024 * 1024, nil // 10 GB free
		},
	}
	coord := multicast.NewCoordinator(cfg)
	ctx := context.Background()

	// 1. Sender simulation
	senderProgressCalled := false
	err := coord.RunSender(ctx, multicast.SenderParams{
		TaskID:   "task_test_01",
		FilePath: "base.vhdx",
		ProgressCallback: func(p, s float64) {
			senderProgressCalled = true
		},
	})
	if err != nil {
		t.Fatalf("sender failed in dryrun: %v", err)
	}
	if !senderProgressCalled {
		t.Fatalf("expected sender progress callback to be called")
	}

	// 2. Receiver simulation (success case)
	receiverProgressCalled := false
	result, err := coord.RunReceiver(ctx, multicast.ReceiverParams{
		TaskID:         "task_test_01",
		MasterIP:       "192.168.1.100",
		ExpectedSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ExpectedBytes:  1024 * 1024,
		OutputPath:     "base.vhdx.new",
		ProgressCallback: func(p, s float64) {
			receiverProgressCalled = true
		},
	})
	if err != nil {
		t.Fatalf("receiver failed in dryrun: %v", err)
	}
	if !receiverProgressCalled || !result.OK {
		t.Fatalf("expected successful receiver result, got %+v", result)
	}

	// 3. Receiver simulation (checksum mismatch case -> IMAGE_VERIFY_FAILED)
	failResult, err := coord.RunReceiver(ctx, multicast.ReceiverParams{
		TaskID:         "task_test_fail",
		MasterIP:       "192.168.1.100",
		ExpectedSHA256: "FAIL_HASH_TRIGGER_MISMATCH",
		ExpectedBytes:  1024 * 1024,
		OutputPath:     "base.vhdx.new",
	})
	if err == nil {
		t.Fatalf("expected verify error on mismatch hash, got nil")
	}
	if failResult.OK || failResult.ErrorCode != protocol.ErrImageVerifyFailed {
		t.Fatalf("expected error code IMAGE_VERIFY_FAILED, got %+v", failResult)
	}
}

