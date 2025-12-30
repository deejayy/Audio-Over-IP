package wcatools

import (
	"runtime"

	"github.com/go-ole/go-ole"
)

// InitCOM initializes COM on the current OS thread. Caller should lock the
// goroutine to the OS thread (runtime.LockOSThread) before calling this if the
// thread needs to be kept stable for COM usage.
func InitCOM() error {
	// Initialize COM for this thread.
	if err := ole.CoInitialize(0); err != nil {
		return err
	}
	return nil
}

// UninitCOM uninitializes COM on the current OS thread.
func UninitCOM() {
	ole.CoUninitialize()
	// If the caller locked the OS thread, they can call runtime.UnlockOSThread after this.
	runtime.Gosched()
}
