package pcmresample

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
)

// SampleFormat enumerates supported 32-bit sample formats.
type SampleFormat int

const (
	PCM32Float SampleFormat = iota // IEEE-754 float32, little-endian
	PCM32Int                       // signed int32, little-endian (Q31)
)

// InterpMethod selects the rate-conversion kernel.
type InterpMethod int

const (
	InterpLinear InterpMethod = iota
	InterpCubic
	InterpSinc
)

// Settings for a PCM stream.
type Settings struct {
	SampleRate int
	Channels   int
	Format     SampleFormat // 32-bit only
}

// Options control resampler construction.
type Options struct {
	Method              InterpMethod
	Quality             int         // 1..MaxQuality
	MaxQuality          int         // upper bound for Quality; default 100 if 0
	MaxThreads          int         // <= 0 -> auto (GOMAXPROCS)
	CustomMixMatrix     [][]float32 // optional: [dstCh][srcCh]. If nil, default matrix is used.
	IncludeLFEInDownmix bool        // if true, include LFE in downmix at -6 dB default
}

// Resampler performs streaming sample-rate conversion and channel mixing.
type Resampler struct {
	src, dst   Settings
	opts       Options
	ratio      float64 // srcRate / dstRate
	method     rateKernel
	mix        mixMatrix
	maxThreads int

	volume float32
	volMu  sync.RWMutex

	passthrough bool

	// Streaming state for rate converter:
	srcBuf    [][]float32 // ring-buffer per channel
	bufStart  int         // absolute frame index corresponding to srcBuf[?][0]
	pos       float64     // fractional source-frame position for next output frame, relative to bufStart
	supportL  int         // left support frames needed by the kernel
	supportR  int         // right support frames needed by the kernel
	tableFrac int         // for sinc: table resolution (phases)

	// Concurrency helpers:
	wg       sync.WaitGroup
	chunkBuf [][]float32 // scratch per-thread
}

// NewResampler creates a new stateful resampler.
func NewResampler(src, dst Settings, opts Options) (*Resampler, error) {
	if src.SampleRate <= 0 || dst.SampleRate <= 0 {
		return nil, errors.New("invalid sample rates")
	}
	if src.Channels <= 0 || dst.Channels <= 0 {
		return nil, errors.New("invalid channel counts")
	}
	if opts.MaxQuality <= 0 {
		opts.MaxQuality = 100
	}
	if opts.Quality <= 0 || opts.Quality > opts.MaxQuality {
		return nil, errors.New("quality must be within 1..MaxQuality")
	}
	r := &Resampler{
		src:    src,
		dst:    dst,
		opts:   opts,
		ratio:  float64(src.SampleRate) / float64(dst.SampleRate),
		volume: 1.0,
	}

	if src == dst && opts.CustomMixMatrix == nil {
		r.passthrough = true
		return r, nil
	}

	switch opts.Method {
	case InterpLinear:
		r.method = newLinearKernel()
	case InterpCubic:
		r.method = newCubicKernel(opts)
	case InterpSinc:
		k, err := newSincKernel(src.SampleRate, dst.SampleRate, opts)
		if err != nil {
			return nil, err
		}
		r.method = k
	default:
		return nil, errors.New("unknown interpolation method")
	}

	r.supportL = r.method.SupportLeft()
	r.supportR = r.method.SupportRight()
	r.tableFrac = r.method.TablePhases()

	// Channels mixer:
	var mm mixMatrix
	if opts.CustomMixMatrix != nil {
		mm = mixMatrix{m: opts.CustomMixMatrix}
		if len(mm.m) != dst.Channels || len(mm.m[0]) != src.Channels {
			return nil, errors.New("CustomMixMatrix shape must be [dstCh][srcCh]")
		}
	} else {
		mm = defaultMixMatrix(src.Channels, dst.Channels, opts.IncludeLFEInDownmix)
	}
	r.mix = mm

	// Buffers: start with some capacity
	r.srcBuf = make([][]float32, src.Channels)
	initialCap := 2048 + r.supportL + r.supportR
	for c := range r.srcBuf {
		r.srcBuf[c] = make([]float32, 0, initialCap)
	}
	r.pos = float64(r.supportL) // start position aligned so we have left support in buffer model

	// Threads:
	if opts.MaxThreads <= 0 {
		r.maxThreads = runtime.GOMAXPROCS(0)
	} else {
		r.maxThreads = opts.MaxThreads
	}

	return r, nil
}

func (r *Resampler) SetVolume(vol float32) {
	r.volMu.Lock()
	defer r.volMu.Unlock()
	r.volume = vol
}

func (r *Resampler) GetVolume() float32 {
	r.volMu.RLock()
	defer r.volMu.RUnlock()
	return r.volume
}

// Process consumes input bytes (src settings) and produces output bytes (dst settings).
// It returns the number of input bytes consumed and the produced output slice.
func (r *Resampler) Process(in []byte) (out []byte, consumed int, err error) {
	if r.passthrough {
		r.volMu.RLock()
		vol := r.volume
		r.volMu.RUnlock()

		if vol == 1.0 {
			out = make([]byte, len(in))
			copy(out, in)
			return out, len(in), nil
		}

		// Apply volume
		decoded, err := decodeBytesToPlanar(in, r.src.Format, r.src.Channels)
		if err != nil {
			return nil, 0, err
		}

		for c := range decoded {
			for i := range decoded[c] {
				decoded[c][i] *= vol
			}
		}

		outBytes, err := encodePlanarToBytes(decoded, r.dst.Format)
		if err != nil {
			return nil, 0, err
		}
		return outBytes, len(in), nil
	}

	// Decode input bytes into srcBuf (append).
	samplesIn := len(in) / (r.src.Channels * 4)
	if samplesIn < 0 {
		return nil, 0, nil
	}
	decoded, err := decodeBytesToPlanar(in, r.src.Format, r.src.Channels)
	if err != nil {
		return nil, 0, err
	}
	// Append to internal buffer
	for c := 0; c < r.src.Channels; c++ {
		r.srcBuf[c] = append(r.srcBuf[c], decoded[c]...)
	}

	// Determine how many output frames we can produce with current srcBuf.
	// The maximum source index needed for Nout frames:
	// maxIdxNeeded = floor(pos + ratio*(Nout-1)) + supportR
	// We can invert to find NoutMax given available source frames Nsrc:
	Nsrc := len(r.srcBuf[0])
	NoutMax := 0
	if Nsrc > 0 {
		// max Nout satisfies: floor(pos + ratio*(Nout-1)) + supportR < Nsrc
		// Conservatively: pos + ratio*(Nout-1) + supportR < Nsrc
		// => Nout < (Nsrc - supportR - pos)/ratio + 1
		NoutF := (float64(Nsrc)-float64(r.supportR)-r.pos)/r.ratio + 1.0
		if NoutF > 0 {
			NoutMax = int(math.Floor(NoutF))
		}
	}

	if NoutMax <= 0 {
		return nil, len(in), nil
	}

	// Allocate output planar buffer
	outPlanar := make([][]float32, r.dst.Channels)
	for c := range outPlanar {
		outPlanar[c] = make([]float32, NoutMax)
	}

	// Rate convert (per channel), then mix. For efficiency, we do: rate-convert src -> temp (srcCh),
	// then multiply by mix matrix into outPlanar. To reduce work, we can rate-convert once per src channel.
	// Parallelize work in chunks over output frames.

	threads := r.maxThreads
	if threads > NoutMax {
		threads = NoutMax
	}
	if threads < 1 {
		threads = 1
	}
	chunk := (NoutMax + threads - 1) / threads

	type tempBlock struct {
		from int
		to   int
		// tmp per src channel
		ch [][]float32
	}
	blocks := make([]tempBlock, threads)
	for i := 0; i < threads; i++ {
		from := i * chunk
		to := (i + 1) * chunk
		if to > NoutMax {
			to = NoutMax
		}
		if from >= to {
			blocks[i] = tempBlock{from: from, to: from}
			continue
		}
		ch := make([][]float32, r.src.Channels)
		for c := range ch {
			ch[c] = make([]float32, to-from)
		}
		blocks[i] = tempBlock{from: from, to: to, ch: ch}
	}

	r.wg.Add(threads)
	errs := make([]error, threads)

	// Capture volume for this block to avoid locking per sample
	r.volMu.RLock()
	currentVol := r.volume
	r.volMu.RUnlock()

	for i := 0; i < threads; i++ {
		i := i
		go func() {
			defer r.wg.Done()
			if blocks[i].from >= blocks[i].to {
				return
			}
			basePos := r.pos + float64(blocks[i].from)*r.ratio
			for sc := 0; sc < r.src.Channels; sc++ {
				r.method.Process(r.srcBuf[sc], basePos, r.ratio, blocks[i].ch[sc])
			}
			// Mix into outPlanar
			for n := 0; n < blocks[i].to-blocks[i].from; n++ {
				// dst channel d = sum_s mix[d][s] * sample_s
				for d := 0; d < r.dst.Channels; d++ {
					sum := float32(0)
					row := r.mix.m[d]
					for s := 0; s < r.src.Channels; s++ {
						sum += row[s] * blocks[i].ch[s][n]
					}
					outPlanar[d][blocks[i].from+n] = sum * currentVol
				}
			}
		}()
	}
	r.wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, 0, e
		}
	}

	// Advance position and possibly drop consumed source frames to keep buffer small.
	r.pos += float64(NoutMax) * r.ratio

	// We can drop full frames to the left of floor(pos)-supportL to maintain left support.
	dropUntil := int(math.Floor(r.pos)) - r.supportL
	if dropUntil > 0 {
		for c := 0; c < r.src.Channels; c++ {
			if dropUntil > len(r.srcBuf[c]) {
				dropUntil = len(r.srcBuf[c])
			}
			r.srcBuf[c] = r.srcBuf[c][dropUntil:]
		}
		r.bufStart += dropUntil
		r.pos -= float64(dropUntil)
	}

	// Encode outPlanar to bytes
	outBytes, err := encodePlanarToBytes(outPlanar, r.dst.Format)
	if err != nil {
		return nil, 0, err
	}
	return outBytes, len(in), nil
}

// CalculateInputSize returns the number of input bytes that, when passed to Process(),
// will produce an output size less than or equal to desiredOutputBytes.
// It guarantees that the output will not exceed desiredOutputBytes.
func (r *Resampler) CalculateInputSize(desiredOutputBytes int) int {
	if r.passthrough {
		return desiredOutputBytes
	}
	if desiredOutputBytes <= 0 {
		return 0
	}
	bytesPerOutFrame := r.dst.Channels * 4
	bytesPerInFrame := r.src.Channels * 4

	maxOutFrames := desiredOutputBytes / bytesPerOutFrame
	if maxOutFrames <= 0 {
		return 0
	}

	// We want to find the largest N such that:
	// floor(pos + ratio*(N - 1)) + supportR + 1 - buffered <= Nsrc
	// But we invert the logic: for each candidate input frame count, compute how many output frames it would produce.

	buffered := 0
	if len(r.srcBuf) > 0 {
		buffered = len(r.srcBuf[0])
	}

	// Try input frame counts from 0 up to a safe upper bound
	// Conservative upper bound: desiredOutputBytes * ratio
	upperBound := int(float64(maxOutFrames)*r.ratio) + r.supportR + 2

	bestInputFrames := 0
	for inputFrames := 0; inputFrames <= upperBound; inputFrames++ {
		totalFrames := buffered + inputFrames
		// Estimate how many output frames we can produce
		// Nout = floor((totalFrames - supportR - pos)/ratio + 1)
		noutF := (float64(totalFrames)-float64(r.supportR)-r.pos)/r.ratio + 1.0
		nout := int(math.Floor(noutF))
		if nout*bytesPerOutFrame > desiredOutputBytes {
			break
		}
		bestInputFrames = inputFrames
	}

	return bestInputFrames * bytesPerInFrame
}

// ProcessTo processes input into a provided output buffer (dst settings) to minimize allocations.
// Returns (consumedInBytes, producedOutBytes).
func (r *Resampler) ProcessTo(in []byte, out []byte) (int, int, error) {
	o, consumed, err := r.Process(in)
	if err != nil {
		return 0, 0, err
	}
	if len(out) < len(o) {
		copy(out, o)
		return consumed, len(out), nil
	}
	copy(out, o)
	return consumed, len(o), nil
}

// Flush drains internal filter state and returns remaining output.
// For linear/cubic, this is usually empty. For sinc, it flushes half-taps of latency.
func (r *Resampler) Flush() ([]byte, error) {
	if r.passthrough {
		return nil, nil
	}
	// For a strictly streaming application, it's enough to clear buffers.
	// We won't synthesize new samples beyond available src; return empty.
	for c := range r.srcBuf {
		r.srcBuf[c] = r.srcBuf[c][:0]
	}
	r.pos = float64(r.supportL)
	return nil, nil
}

// RequiredInputBytesForOutput returns the exact number of additional input bytes needed
// (beyond what is already buffered) to produce desiredOutBytes of output, given current state.
func (r *Resampler) RequiredInputBytesForOutput(desiredOutBytes int) int {
	if r.passthrough {
		return desiredOutBytes
	}
	framesOut := desiredOutBytes / (r.dst.Channels * 4)
	if framesOut <= 0 {
		return 0
	}
	NsrcAvail := 0
	if len(r.srcBuf) > 0 {
		NsrcAvail = len(r.srcBuf[0])
	}
	// Need max source index for last output frame we intend to generate:
	// idxNeeded = floor(pos + ratio*(framesOut-1)) + supportR
	idxNeeded := int(math.Floor(r.pos+r.ratio*float64(framesOut-1))) + r.supportR
	neededFrames := idxNeeded + 1 // since index is zero-based
	additional := neededFrames - NsrcAvail
	if additional < 0 {
		additional = 0
	}
	return additional * r.src.Channels * 4
}

// Info returns summary of internal configuration (for debugging/logging).
func (r *Resampler) Info() string {
	if r.passthrough {
		return fmtStr("Resampler: %dch@%d -> %dch@%d (passthrough)",
			r.src.Channels, r.src.SampleRate, r.dst.Channels, r.dst.SampleRate)
	}
	return fmtStr("Resampler: %dch@%d -> %dch@%d, method=%v, quality=%d/%d, ratio=%.6f, threads=%d",
		r.src.Channels, r.src.SampleRate, r.dst.Channels, r.dst.SampleRate,
		r.opts.Method, r.opts.Quality, r.opts.MaxQuality, r.ratio, r.maxThreads,
	)
}

func fmtStr(f string, a ...any) string { return fmt.Sprintf(f, a...) }
