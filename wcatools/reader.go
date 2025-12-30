package wcatools

import (
	"fmt"
	"unsafe"

	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

const (
	waitObject0 = uint32(0)
	waitTimeout = uint32(0x102)
)

type DeviceReader struct {
	client     *wca.IAudioClient
	capture    *wca.IAudioCaptureClient
	blockAlign int
	mixFormat  *wca.WAVEFORMATEX
	event      windows.Handle
	readyCh    chan struct{}
	stopCh     chan struct{}
}

// NewDeviceReader initializes an event-driven reader for the given device ID.
func NewDeviceReader(deviceID string) (*DeviceReader, error) {
	// Create IMMDeviceEnumerator
	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		return nil, fmt.Errorf("failed to create device enumerator: %w", err)
	}
	defer enumerator.Release()

	// Enumerate endpoints and find the device with matching ID
	var collection *wca.IMMDeviceCollection
	if err := enumerator.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &collection); err != nil {
		return nil, fmt.Errorf("failed to enumerate devices: %w", err)
	}
	defer collection.Release()

	var count uint32
	if err := collection.GetCount(&count); err != nil {
		return nil, err
	}

	var device *wca.IMMDevice
	for i := uint32(0); i < count; i++ {
		var d *wca.IMMDevice
		if err := collection.Item(i, &d); err != nil {
			continue
		}

		var id string
		if err := d.GetId(&id); err != nil {
			d.Release()
			continue
		}
		if id == deviceID {
			device = d
			// found - take ownership, don't release here
			break
		}
		// not our device, release immediately
		d.Release()
	}
	if device == nil {
		return nil, fmt.Errorf("device not found: %s", deviceID)
	}

	var client *wca.IAudioClient
	if err := device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &client); err != nil {
		return nil, err
	}

	var format *wca.WAVEFORMATEX
	if err := client.GetMixFormat(&format); err != nil {
		return nil, err
	}

	// Request ~10ms buffer
	hnsBufferDuration := 1_000_000 // 10ms in 100ns units
	if err := client.Initialize(
		wca.AUDCLNT_SHAREMODE_SHARED,
		wca.AUDCLNT_STREAMFLAGS_LOOPBACK|wca.AUDCLNT_STREAMFLAGS_EVENTCALLBACK,
		wca.REFERENCE_TIME(hnsBufferDuration),
		0,
		format,
		nil,
	); err != nil {
		return nil, fmt.Errorf("failed to initialize audio client: %w", err)
	}

	var capture *wca.IAudioCaptureClient
	if err := client.GetService(wca.IID_IAudioCaptureClient, &capture); err != nil {
		return nil, err
	}

	// Create event handle (auto-reset = false, manualReset bool = false -> bManualReset=0)
	// Use proper types for CreateEvent: SECURITY_ATTRIBUTES=nil, bManualReset=false, bInitialState=false
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		// cleanup activated client before returning
		client.Release()
		return nil, fmt.Errorf("failed to create event: %w", err)
	}
	if err := client.SetEventHandle(uintptr(event)); err != nil {
		windows.CloseHandle(event)
		client.Release()
		return nil, fmt.Errorf("failed to set event handle: %w", err)
	}

	dr := &DeviceReader{
		client:     client,
		capture:    capture,
		blockAlign: int(format.NBlockAlign),
		mixFormat:  format,
		event:      event,
		readyCh:    make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
	}

	// Start background waiter
	go dr.waitLoop()

	// Start audio engine
	if err := client.Start(); err != nil {
		return nil, err
	}

	return dr, nil
}

// waitLoop waits for the event and signals readyCh.
func (r *DeviceReader) waitLoop() {
	for {
		select {
		case <-r.stopCh:
			return
		default:
			// Wait with a timeout so we can check stopCh periodically.
			s := wca.WaitForSingleObject(uintptr(r.event), 500) // 500ms
			switch s {
			case waitObject0:
				select {
				case r.readyCh <- struct{}{}:
				default:
					// drop if channel is full
				}
			case waitTimeout:
				// timeout
			default:
				fmt.Printf("[dbg] WaitForSingleObject returned: %d\n", s)
			}
		}
	}
}

// Ready returns a channel that signals when audio is available.
func (r *DeviceReader) Ready() <-chan struct{} {
	return r.readyCh
}

// MixFormat returns the device mix format (WAVEFORMATEX) from GetMixFormat.
func (r *DeviceReader) MixFormat() *wca.WAVEFORMATEX {
	return r.mixFormat
}

// Read implements io.Reader to stream audio bytes.
// Read returns a newly allocated byte slice containing whole frames of audio
// (no partial frames). It never writes into a caller-provided buffer.
func (r *DeviceReader) Read() ([]byte, error) {
	var numFramesInNextPacket uint32
	if err := r.capture.GetNextPacketSize(&numFramesInNextPacket); err != nil {
		fmt.Printf("[err] GetNextPacketSize failed: type=%T val=%v\n", err, err)
		if e, ok := err.(windows.Errno); ok {
			fmt.Printf("[err] code hex=0x%X dec=%d\n", uint32(e), uint32(e))
		}
		return nil, err
	}

	if numFramesInNextPacket == 0 {
		return nil, nil
	}

	var data *byte
	var numFrames uint32
	var flags uint32

	if err := r.capture.GetBuffer(&data, &numFrames, &flags, nil, nil); err != nil {
		fmt.Printf("[err] GetBuffer failed: type=%T val=%v\n", err, err)
		if e, ok := err.(windows.Errno); ok {
			fmt.Printf("[err] code hex=0x%X dec=%d\n", uint32(e), uint32(e))
		}
		return nil, err
	}

	totalBytes := int(numFrames) * r.blockAlign

	// Should be impossible since we already checked numFramesInNextPacket, so GetBuffer should have to return data
	// Just a safety fallback
	if totalBytes == 0 {
		if err := r.capture.ReleaseBuffer(numFrames); err != nil {
			fmt.Printf("[err] ReleaseBuffer failed (zero frames): type=%T val=%v\n", err, err)
			return nil, err
		}
		return nil, nil
	}

	// Create a safe copy of the data before releasing the buffer
	// unsafe.Slice creates a slice header pointing to the raw memory.
	// We must copy it to a Go-managed allocation because ReleaseBuffer invalidates the raw memory.
	rawSlice := unsafe.Slice(data, totalBytes)
	audioData := make([]byte, len(rawSlice))

	// Check for silence flag
	if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0 {
		// Data is undefined; write silence.
		// make() already zeroed audioData, so we don't need to do anything.
	} else {
		copy(audioData, rawSlice)
	}

	// Always release the full number of frames reported by GetBuffer.
	if err := r.capture.ReleaseBuffer(numFrames); err != nil {
		fmt.Printf("[err] ReleaseBuffer failed: type=%T val=%v\n", err, err)
		if e, ok := err.(windows.Errno); ok {
			fmt.Printf("[err] code hex=0x%X dec=%d\n", uint32(e), uint32(e))
		}
		return nil, err
	}

	if len(audioData) == 0 {
		return nil, nil
	}

	return audioData, nil
}

// Close stops the client and releases resources.
func (r *DeviceReader) Close() error {
	close(r.stopCh)
	if err := r.client.Stop(); err != nil {
		return err
	}
	r.capture.Release()
	r.client.Release()
	windows.CloseHandle(r.event)
	return nil
}
