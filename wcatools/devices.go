package wcatools

import (
	"errors"
	"fmt"

	"github.com/moutend/go-wca/pkg/wca"
)

type AudioConfig struct {
	ID             string
	Name           string
	SamplesPerSec  uint32 `json:"SamplesPerSec"`
	BitsPerSample  uint16 `json:"BitsPerSample"`
	Channels       uint16 `json:"Channels"`
	BlockAlign     uint16 `json:"BlockAlign"`
	AvgBytesPerSec uint32 `json:"AvgBytesPerSec"`
	FormatTag      uint16 `json:"FormatTag"`
	CbSize         uint16 `json:"CbSize"`
}

// ListDevices enumerates all active audio devices and returns their properties.
func ListDevices() ([]AudioConfig, error) {
	var devices []AudioConfig

	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		return nil, fmt.Errorf("failed to create device enumerator: %w", err)
	}
	defer enumerator.Release()

	var collection *wca.IMMDeviceCollection
	if err := enumerator.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &collection); err != nil {
		return nil, fmt.Errorf("failed to enumerate devices: %w", err)
	}
	defer collection.Release()

	var count uint32
	if err := collection.GetCount(&count); err != nil {
		return nil, err
	}

	for i := uint32(0); i < count; i++ {
		var device *wca.IMMDevice
		if err := collection.Item(i, &device); err != nil {
			continue
		}

		// get ID
		var id string
		if err := device.GetId(&id); err != nil {
			device.Release()
			continue
		}

		// friendly name
		var store *wca.IPropertyStore
		if err := device.OpenPropertyStore(wca.STGM_READ, &store); err != nil {
			device.Release()
			continue
		}

		var pv wca.PROPVARIANT
		if err := store.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err != nil {
			store.Release()
			device.Release()
			continue
		}
		name := pv.String()
		store.Release()

		// Activate IAudioClient
		var client *wca.IAudioClient
		if err := device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &client); err != nil {
			device.Release()
			continue
		}

		var mixFormat *wca.WAVEFORMATEX
		if err := client.GetMixFormat(&mixFormat); err != nil {
			client.Release()
			device.Release()
			continue
		}
		client.Release()

		devices = append(devices, AudioConfig{
			ID:            id,
			Name:          name,
			Channels:      mixFormat.NChannels,
			SamplesPerSec: mixFormat.NSamplesPerSec,
			BitsPerSample: mixFormat.WBitsPerSample,
		})
		device.Release()
	}

	return devices, nil
}

func GetDeviceByID(deviceID string) (AudioConfig, error) {
	allDev, err := ListDevices()
	if err != nil {
		return AudioConfig{}, err
	}
	for _, dev := range allDev {
		if dev.ID == deviceID {
			return dev, nil
		}
	}
	return AudioConfig{}, errors.New("device with id " + deviceID + " does not exist")
}

// GetDefaultDeviceID returns the device ID of the default render endpoint.
// Caller must initialize COM before calling.
func GetDefaultDeviceID() (string, error) {
	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		return "", fmt.Errorf("failed to create device enumerator: %w", err)
	}
	defer enumerator.Release()

	var dev *wca.IMMDevice
	if err := enumerator.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &dev); err != nil {
		return "", err
	}
	defer dev.Release()

	var id string
	if err := dev.GetId(&id); err != nil {
		return "", err
	}
	return id, nil
}

// GetDefaultDeviceInfo returns mix-format fields and friendly name for the
// default render endpoint. Caller must initialize COM before calling.
func GetDefaultDeviceInfo() (AudioConfig, error) {
	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		return AudioConfig{}, fmt.Errorf("failed to create device enumerator: %w", err)
	}
	defer enumerator.Release()

	var dev *wca.IMMDevice
	if err := enumerator.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &dev); err != nil {
		return AudioConfig{}, err
	}
	defer dev.Release()

	var id string
	if err := dev.GetId(&id); err != nil {
		return AudioConfig{}, err
	}

	var ps *wca.IPropertyStore
	if err := dev.OpenPropertyStore(wca.STGM_READ, &ps); err != nil {
		return AudioConfig{}, err
	}
	defer ps.Release()

	var pv wca.PROPVARIANT
	if err := ps.GetValue(&wca.PKEY_Device_FriendlyName, &pv); err != nil {
		return AudioConfig{}, err
	}
	name := pv.String()

	var client *wca.IAudioClient
	if err := dev.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &client); err != nil {
		return AudioConfig{}, err
	}
	defer client.Release()

	var mixFormat *wca.WAVEFORMATEX
	if err := client.GetMixFormat(&mixFormat); err != nil {
		return AudioConfig{}, err
	}

	info := AudioConfig{
		ID:             id,
		Name:           name,
		Channels:       mixFormat.NChannels,
		SamplesPerSec:  mixFormat.NSamplesPerSec,
		BitsPerSample:  mixFormat.WBitsPerSample,
		BlockAlign:     mixFormat.NBlockAlign,
		AvgBytesPerSec: mixFormat.NAvgBytesPerSec,
		FormatTag:      mixFormat.WFormatTag,
		CbSize:         mixFormat.CbSize,
	}

	return info, nil
}

// GetIMMDeviceByID finds an active render device by ID and returns it (AddRef'd).
// Caller must Release() the device.
func GetIMMDeviceByID(deviceID string) (*wca.IMMDevice, error) {
	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		return nil, fmt.Errorf("failed to create device enumerator: %w", err)
	}
	defer enumerator.Release()

	var collection *wca.IMMDeviceCollection
	if err := enumerator.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &collection); err != nil {
		return nil, fmt.Errorf("failed to enumerate devices: %w", err)
	}
	defer collection.Release()

	var count uint32
	if err := collection.GetCount(&count); err != nil {
		return nil, err
	}

	for i := uint32(0); i < count; i++ {
		var device *wca.IMMDevice
		if err := collection.Item(i, &device); err != nil {
			continue
		}

		var id string
		if err := device.GetId(&id); err != nil {
			device.Release()
			continue
		}

		if id == deviceID {
			return device, nil
		}
		device.Release()
	}

	return nil, fmt.Errorf("device with id %s not found", deviceID)
}
