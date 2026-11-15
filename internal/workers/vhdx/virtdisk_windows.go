package vhdx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	virtualStorageTypeDeviceVHDX = 3
	virtualDiskAccessNone        = 0
	createVirtualDiskVersion2    = 2
	defaultSectorSize            = 512
	minVHDXBytes                 = 3 << 20 // CreateVirtualDisk requires >= 3 MB.
)

var (
	virtdiskDLL           = windows.NewLazySystemDLL("virtdisk.dll")
	procCreateVirtualDisk = virtdiskDLL.NewProc("CreateVirtualDisk")
)

// virtualStorageType mirrors VIRTUAL_STORAGE_TYPE from virtdisk.h.
type virtualStorageType struct {
	DeviceID uint32
	VendorID windows.GUID
}

// VIRTUAL_STORAGE_TYPE_VENDOR_MICROSOFT
var microsoftVendorID = windows.GUID{
	Data1: 0xEC984AEC,
	Data2: 0xA0F9,
	Data3: 0x47E9,
	Data4: [8]byte{0x90, 0x1F, 0x71, 0x41, 0x5A, 0x66, 0x34, 0x5B},
}

// createVirtualDiskParameters mirrors CREATE_VIRTUAL_DISK_PARAMETERS
// (CREATE_VIRTUAL_DISK_VERSION_2). The explicit padding keeps the union
// aligned the same way the C struct is aligned.
type createVirtualDiskParameters struct {
	Version                   uint32
	_                         uint32
	UniqueID                  windows.GUID
	MaximumSize               uint64
	BlockSizeInBytes          uint32
	SectorSizeInBytes         uint32
	PhysicalSectorSizeInBytes uint32
	_                         uint32
	ParentPath                *uint16
	SourcePath                *uint16
	OpenFlags                 uint32
	_                         uint32
	ParentVirtualStorageType  virtualStorageType
	SourceVirtualStorageType  virtualStorageType
	ResiliencyGUID            windows.GUID
}

func vhdxStorageType() virtualStorageType {
	return virtualStorageType{DeviceID: virtualStorageTypeDeviceVHDX, VendorID: microsoftVendorID}
}

type virtualDiskBackend struct{}

func newVirtualDiskBackend() DiskBackend {
	return &virtualDiskBackend{}
}

func (b *virtualDiskBackend) CreateBase(ctx context.Context, path string, sizeBytes uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sizeBytes < minVHDXBytes {
		return fmt.Errorf("VHDX size %d is below the minimum of %d bytes", sizeBytes, minVHDXBytes)
	}
	if sizeBytes%defaultSectorSize != 0 {
		return fmt.Errorf("VHDX size %d must be a multiple of %d bytes", sizeBytes, defaultSectorSize)
	}

	absolute, err := prepareNewVHDXPath(path)
	if err != nil {
		return err
	}

	params := createVirtualDiskParameters{
		Version:                  createVirtualDiskVersion2,
		MaximumSize:              sizeBytes,
		BlockSizeInBytes:         0, // default (2 MB)
		SectorSizeInBytes:        defaultSectorSize,
		ParentVirtualStorageType: vhdxStorageType(),
		SourceVirtualStorageType: vhdxStorageType(),
	}
	return createVirtualDisk(absolute, &params)
}

func (b *virtualDiskBackend) CreateDifferencing(ctx context.Context, path, parentPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	absolute, err := prepareNewVHDXPath(path)
	if err != nil {
		return err
	}

	parentAbsolute, err := filepath.Abs(parentPath)
	if err != nil {
		return fmt.Errorf("resolve parent path: %w", err)
	}
	if info, statErr := os.Stat(parentAbsolute); statErr != nil || info.IsDir() {
		return fmt.Errorf("parent VHDX does not exist: %s", parentAbsolute)
	}

	parentPtr, err := windows.UTF16PtrFromString(parentAbsolute)
	if err != nil {
		return fmt.Errorf("encode parent path: %w", err)
	}

	params := createVirtualDiskParameters{
		Version:                  createVirtualDiskVersion2,
		MaximumSize:              0, // required to be zero for a differencing disk
		BlockSizeInBytes:         0,
		SectorSizeInBytes:        defaultSectorSize,
		ParentPath:               parentPtr,
		ParentVirtualStorageType: vhdxStorageType(),
		SourceVirtualStorageType: vhdxStorageType(),
	}
	return createVirtualDisk(absolute, &params)
}

func (b *virtualDiskBackend) Delete(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete VHDX %s: %w", path, err)
	}
	return nil
}

func (b *virtualDiskBackend) Exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func prepareNewVHDXPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve VHDX path: %w", err)
	}
	if _, statErr := os.Stat(absolute); statErr == nil {
		return "", fmt.Errorf("VHDX already exists: %s", absolute)
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("stat VHDX path: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return "", fmt.Errorf("create VHDX directory: %w", err)
	}
	return absolute, nil
}

func createVirtualDisk(path string, params *createVirtualDiskParameters) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode VHDX path: %w", err)
	}

	storageType := vhdxStorageType()
	var handle windows.Handle

	result, _, _ := procCreateVirtualDisk.Call(
		uintptr(unsafe.Pointer(&storageType)),
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(virtualDiskAccessNone),
		0, // security descriptor
		0, // flags
		0, // provider-specific flags
		uintptr(unsafe.Pointer(params)),
		0, // overlapped
		uintptr(unsafe.Pointer(&handle)),
	)
	if result != 0 {
		return fmt.Errorf("CreateVirtualDisk(%s) failed: %w", path, syscall.Errno(result))
	}
	_ = windows.CloseHandle(handle)
	return nil
}
