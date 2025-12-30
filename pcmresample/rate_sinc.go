package pcmresample

import (
	"math"
)

// Windowed-sinc bandlimited interpolation (FIR) with polyphase table.
// Quality controls taps, window beta, and table resolution.
type sincKernel struct {
	taps        int
	half        int
	tablePhases int
	cutoff      float64
	coeffs      [][]float64 // [tablePhases][taps], time-symmetric
}

func newSincKernel(srcRate, dstRate int, opts Options) (rateKernel, error) {
	// Map quality -> parameters. Ensure distinct outputs for all levels.
	// Example mapping (Q in 1..MaxQuality):
	// taps = 16 + 16*Q, phases = 256 + 128*Q, cutoff = 0.93*min(1, dst/src)*(0.98 + 0.002*Q)
	Q := opts.Quality
	taps := 16 + 16*Q
	if taps%2 != 0 {
		taps++ // even for symmetric half
	}
	phases := 256 + 128*Q
	r := float64(dstRate) / float64(srcRate) // upsampling factor in destination domain
	minFactor := math.Min(1.0, r)
	cutoff := 0.93 * minFactor * (0.985 + 0.001*float64(Q))
	beta := 5.0 + 0.7*float64(Q) // Kaiser beta

	coeffs := make([][]float64, phases)
	half := taps / 2
	for p := 0; p < phases; p++ {
		coeffs[p] = designSincPhase(taps, half, cutoff, float64(p)/float64(phases), beta)
		// Normalize each phase to unity gain at DC
		sum := 0.0
		for i := 0; i < taps; i++ {
			sum += coeffs[p][i]
		}
		if sum != 0 {
			for i := 0; i < taps; i++ {
				coeffs[p][i] /= sum
			}
		}
	}
	return &sincKernel{
		taps:        taps,
		half:        half,
		tablePhases: phases,
		cutoff:      cutoff,
		coeffs:      coeffs,
	}, nil
}

func (k *sincKernel) SupportLeft() int  { return k.half }
func (k *sincKernel) SupportRight() int { return k.half - 1 }
func (k *sincKernel) TablePhases() int  { return k.tablePhases }

func (k *sincKernel) Process(src []float32, startPos, ratio float64, dst []float32) {
	pos := startPos
	n := len(dst)
	L := len(src)
	for i := 0; i < n; i++ {
		x := pos
		j := int(math.Floor(x))
		frac := x - float64(j)
		phase := int(frac * float64(k.tablePhases))
		if phase >= k.tablePhases {
			phase = k.tablePhases - 1
		}
		coeff := k.coeffs[phase]

		sum := 0.0
		// Apply centered FIR across taps around j
		start := j - k.half + 1
		for t := 0; t < k.taps; t++ {
			idx := start + t
			if idx < 0 {
				idx = 0
			} else if idx >= L {
				idx = L - 1
			}
			sum += float64(src[idx]) * coeff[t]
		}
		dst[i] = float32(sum)
		pos += ratio
	}
}

func designSincPhase(taps, half int, cutoff, frac, beta float64) []float64 {
	out := make([]float64, taps)
	// t index centered at 0 corresponds to between samples
	// we position fractional offset frac in [0,1)
	for n := -half + 1; n <= half; n++ {
		i := n + half - 1
		// Ideal sinc at (n - frac)
		x := float64(n) - frac
		pi := math.Pi
		val := 1.0
		if x != 0.0 {
			val = math.Sin(2*pi*cutoff*x) / (pi * x)
		} else {
			val = 2 * cutoff
		}
		w := kaiserWindow(float64(i), float64(taps-1), beta)
		out[i] = val * w
	}
	return out
}

// Kaiser window
func kaiserWindow(n, N, beta float64) float64 {
	if N == 0 {
		return 1
	}
	r := 2*n/N - 1
	return I0(beta*math.Sqrt(1-r*r)) / I0(beta)
}

// Zeroth-order modified Bessel function of the first kind
func I0(x float64) float64 {
	ax := math.Abs(x)
	if ax < 3.75 {
		y := x / 3.75
		y *= y
		return 1.0 + y*(3.5156229+y*(3.0899424+y*(1.2067492+y*(0.2659732+y*(0.0360768+y*0.0045813)))))
	}
	y := 3.75 / ax
	ans := (math.Exp(ax) / math.Sqrt(ax)) * (0.39894228 + y*(0.01328592+y*(0.00225319+y*(-0.00157565+
		y*(0.00916281+y*(-0.02057706+y*(0.02635537+y*(-0.01647633+y*0.00392377))))))))
	return ans
}
