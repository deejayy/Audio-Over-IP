package main

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"AuOvIP/pcmresample"
	"AuOvIP/wcatools"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

type AudioDistributor struct {
	subscribers map[*DevicePlayer]chan []byte
	mu          sync.RWMutex
}

func NewAudioDistributor() *AudioDistributor {
	return &AudioDistributor{
		subscribers: make(map[*DevicePlayer]chan []byte),
	}
}

func (d *AudioDistributor) Subscribe(p *DevicePlayer) chan []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch := make(chan []byte, 200) // Buffer for some jitter
	d.subscribers[p] = ch
	return ch
}

func (d *AudioDistributor) Unsubscribe(p *DevicePlayer) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ch, ok := d.subscribers[p]; ok {
		close(ch)
		delete(d.subscribers, p)
	}
}

func (d *AudioDistributor) Broadcast(data []byte) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, ch := range d.subscribers {
		// Non-blocking send to avoid stalling the reader if a device is slow/stuck
		select {
		case ch <- data:
		default:
			// Drop packet.
			// In the future, we might want to log this or deal with this otherwise
		}
	}
}

type DevicePlayer struct {
	config       PlaybackDevice
	server       *Server
	cancel       context.CancelFunc
	ctx          context.Context
	resampleOpts pcmresample.Options
	srcSettings  pcmresample.Settings
	dstSettings  pcmresample.Settings

	resampler   *pcmresample.Resampler
	resamplerMu sync.Mutex
	swapCh      chan *pcmresample.Resampler
}

func NewDevicePlayer(ctx context.Context, cfg PlaybackDevice, s *Server, src pcmresample.Settings) (*DevicePlayer, error) {
	ctx, cancel := context.WithCancel(ctx)

	// We can't know exact device format without activating it.
	// So we temporarily activate device here to get format and nothing else
	// Initialize COM for this thread
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := wcatools.InitCOM(); err != nil {
		cancel()
		return nil, fmt.Errorf("InitCOM failed: %v", err)
	}
	defer wcatools.UninitCOM()

	targetID := cfg.DeviceID
	if cfg.IsDefault {
		if defID, err := wcatools.GetDefaultDeviceID(); err == nil {
			targetID = defID
		}
	}

	// Get format
	device, err := wcatools.GetIMMDeviceByID(targetID)
	if err != nil {
		cancel()
		return nil, err
	}
	defer device.Release()

	var ac *wca.IAudioClient
	if err := device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		cancel()
		return nil, err
	}
	defer ac.Release()

	var wfx *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&wfx); err != nil {
		cancel()
		return nil, err
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))

	dst := pcmresample.Settings{
		SampleRate: int(wfx.NSamplesPerSec),
		Channels:   int(wfx.NChannels),
		Format:     pcmresample.PCM32Float,
	}

	resampler, err := pcmresample.NewResampler(src, dst, s.ResampleOpts)
	if err != nil {
		cancel()
		return nil, err
	}
	resampler.SetVolume(cfg.Volume)

	return &DevicePlayer{
		config:       cfg,
		server:       s,
		ctx:          ctx,
		cancel:       cancel,
		resampleOpts: s.ResampleOpts,
		srcSettings:  src,
		dstSettings:  dst,
		resampler:    resampler,
		swapCh:       make(chan *pcmresample.Resampler),
	}, nil
}

func (dp *DevicePlayer) Run(inputCh <-chan []byte) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Initialize COM
	if err := wcatools.InitCOM(); err != nil {
		clientLogger.Errorf("DevicePlayer InitCOM failed: %v", err)
		return err
	}
	defer wcatools.UninitCOM()

	// Resolve Device ID (Handle Default)
	targetID := dp.config.DeviceID
	if dp.config.IsDefault {
		defID, err := wcatools.GetDefaultDeviceID()
		if err != nil {
			clientLogger.Errorf("DevicePlayer GetDefaultDeviceID failed: %v", err)
			return err
		}
		targetID = defID
	}

	// Activate Device
	device, err := wcatools.GetIMMDeviceByID(targetID)
	if err != nil {
		clientLogger.Errorf("DevicePlayer GetIMMDeviceByID(%s) failed: %v", targetID, err)
		return err
	}
	defer device.Release()

	// Could get Friendly Name for logging here in the future
	// Skipping for now, trusting the ID works

	// Activate Audio Client
	var ac3 *wca.IAudioClient3
	if err := device.Activate(wca.IID_IAudioClient3, wca.CLSCTX_ALL, nil, &ac3); err != nil {
		clientLogger.Errorf("Activate IAudioClient3 failed: %v", err)
		return err
	}
	defer ac3.Release()

	var wfx *wca.WAVEFORMATEX
	if err := ac3.GetMixFormat(&wfx); err != nil {
		clientLogger.Errorf("GetMixFormat failed: %v", err)
		return err
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))

	// Initialize Audio Stream
	var defaultPeriod, fundamentalPeriod, minPeriod, maxPeriod uint32
	if err := ac3.GetSharedModeEnginePeriod(wfx, &defaultPeriod, &fundamentalPeriod, &minPeriod, &maxPeriod); err != nil {
		return err
	}

	if err := ac3.InitializeSharedAudioStream(wca.AUDCLNT_SHAREMODE_SHARED, defaultPeriod, wfx, nil); err != nil {
		clientLogger.Errorf("InitializeSharedAudioStream failed: %v", err)
		return err
	}

	var bufferFrameSize uint32
	if err := ac3.GetBufferSize(&bufferFrameSize); err != nil {
		return err
	}

	var arc *wca.IAudioRenderClient
	if err := ac3.GetService(wca.IID_IAudioRenderClient, &arc); err != nil {
		return err
	}
	defer arc.Release()

	if err := ac3.Start(); err != nil {
		return err
	}
	defer ac3.Stop()

	// Resampler is already initialized in NewDevicePlayer
	// Just ensure we have the local reference
	dp.resamplerMu.Lock()
	resampler := dp.resampler
	dp.resamplerMu.Unlock()

	if resampler == nil {
		return fmt.Errorf("resampler not initialized")
	}

	var data *byte
	var padding uint32
	var availableFrameSize uint32
	var start = unsafe.Pointer(data)
	var lim int
	var buf []byte

	// Input buffer from channel
	var recvBuf []byte

	// Main Loop
	for {
		select {
		case <-dp.ctx.Done():
			return dp.ctx.Err()
		case newResampler := <-dp.swapCh:
			// Apply current volume to new resampler
			newResampler.SetVolume(dp.config.Volume)

			dp.resamplerMu.Lock()
			dp.resampler = newResampler
			dp.resamplerMu.Unlock()
			// Update local reference for loop
			resampler = newResampler
			clientLogger.Debugf("Resampler swapped for device %s", dp.config.Name)
		default:
		}

		// Render Logic
		if err = ac3.GetCurrentPadding(&padding); err != nil {
			time.Sleep(time.Millisecond)
			continue
		}
		availableFrameSize = bufferFrameSize - padding
		if err = arc.GetBuffer(availableFrameSize, &data); err != nil {
			time.Sleep(time.Millisecond)
			continue
		}
		if data == nil {
			time.Sleep(time.Millisecond)
			continue
		}

		start = unsafe.Pointer(data)
		lim = int(availableFrameSize) * int(wfx.NBlockAlign)

		// Zero-fill
		outSlice := unsafe.Slice((*byte)(start), lim)
		for i := range outSlice {
			outSlice[i] = 0
		}

		inputByteCountNeeded := resampler.CalculateInputSize(lim)
		buf = make([]byte, inputByteCountNeeded)

		if lim == 0 || inputByteCountNeeded == 0 {
			arc.ReleaseBuffer(0, 0)
			time.Sleep(time.Millisecond)
			continue
		}

		// Fill buf from inputCh
		// We need to read EXACTLY or UP TO `inputByteCountNeeded` from `recvBuf` + `inputCh`

		// 1. Drain inputCh into recvBuf until we have enough or channel empty
	drain:
		for len(recvBuf) < inputByteCountNeeded {
			select {
			case chunk, ok := <-inputCh:
				if !ok {
					return nil // Channel closed
				}
				recvBuf = append(recvBuf, chunk...)
			default:
				break drain
			}
		}

		// 2. If still not enough, wait a bit
		if len(recvBuf) == 0 {
			// Wait a bit for data
			select {
			case <-dp.ctx.Done():
				return nil
			case chunk, ok := <-inputCh:
				if !ok {
					return nil
				}
				recvBuf = append(recvBuf, chunk...)
			case <-time.After(2 * time.Millisecond):
			}
		}

		bytesToTake := inputByteCountNeeded
		if len(recvBuf) < bytesToTake {
			bytesToTake = len(recvBuf)
		}

		// Align to frames
		frameSize := int(dp.srcSettings.Channels) * 4
		frames := bytesToTake / frameSize
		bytesToTake = frames * frameSize

		copy(buf, recvBuf[:bytesToTake])
		if len(recvBuf) == bytesToTake {
			recvBuf = recvBuf[:0]
		} else {
			recvBuf = recvBuf[bytesToTake:]
		}

		resampled, _, err := resampler.Process(buf[:bytesToTake])
		if err != nil {
			clientLogger.Errorf("Resample process failed: %v", err)
			continue
		}

		copy(unsafe.Slice((*byte)(start), lim), resampled)

		arc.ReleaseBuffer(min(availableFrameSize, uint32(len(resampled)/int(wfx.NBlockAlign))), 0)
	}
}

func (dp *DevicePlayer) SetVolume(vol float32) {
	dp.resamplerMu.Lock()
	defer dp.resamplerMu.Unlock()
	dp.config.Volume = vol
	if dp.resampler != nil {
		dp.resampler.SetVolume(vol)
	}
}

func (dp *DevicePlayer) UpdateResamplerAsync(opts pcmresample.Options) <-chan error {
	done := make(chan error, 1)

	go func() {
		defer close(done)

		// If dstSettings is empty, we can't create resampler.
		if dp.dstSettings.SampleRate == 0 {
			dp.resampleOpts = opts
			return
		}

		newResampler, err := pcmresample.NewResampler(dp.srcSettings, dp.dstSettings, opts)
		if err != nil {
			done <- err
			return
		}

		select {
		case dp.swapCh <- newResampler:
			// Success
		case <-time.After(5 * time.Second):
			// Timeout (player might be stuck or stopped)
			done <- fmt.Errorf("timeout waiting for player to accept new resampler")
		}
	}()

	return done
}
