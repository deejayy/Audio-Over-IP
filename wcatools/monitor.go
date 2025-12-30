package wcatools

import (
	"runtime"
	"time"
)

type DeviceEvent int

const (
	DeviceAdded DeviceEvent = iota
	DeviceRemoved
	DefaultChanged
)

type MonitorCallback func(event DeviceEvent, deviceID string)

// StartMonitor periodically checks for device changes and calls the callback.
// Returns a cancel function.
func StartMonitor(callback MonitorCallback) func() {
	stop := make(chan struct{})
	
	go func() {
		// Lock thread for COM
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		
		if err := InitCOM(); err != nil {
			return
		}
		defer UninitCOM()

		var lastDevices []AudioConfig
		var lastDefaultID string
		
		// Initial state
		lastDevices, _ = ListDevices()
		lastDefaultID, _ = GetDefaultDeviceID()
		
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				currentDevices, err := ListDevices()
				if err != nil {
					continue
				}
				currentDefaultID, err := GetDefaultDeviceID()
				if err != nil {
					continue
				}

				// Check for Default Change
				if currentDefaultID != lastDefaultID {
					callback(DefaultChanged, currentDefaultID)
					lastDefaultID = currentDefaultID
				}

				// Check for Added
				currentMap := make(map[string]bool)
				for _, d := range currentDevices {
					currentMap[d.ID] = true
					found := false
					for _, old := range lastDevices {
						if old.ID == d.ID {
							found = true
							break
						}
					}
					if !found {
						callback(DeviceAdded, d.ID)
					}
				}

				// Check for Removed
				for _, old := range lastDevices {
					if !currentMap[old.ID] {
						callback(DeviceRemoved, old.ID)
					}
				}

				lastDevices = currentDevices
			}
		}
	}()

	return func() {
		close(stop)
	}
}
