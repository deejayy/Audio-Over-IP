package pcmresample

import "math"

// Cubic kernel using Catmull-Rom interpolation with tension T varied by quality,
// plus a slight postGain to differentiate quality levels and reduce clip risk.
type cubicKernel struct {
	T        float32 // tension parameter in [-1,1]
	postGain float32
}

func newCubicKernel(opts Options) rateKernel {
	// Map quality 1..MaxQuality into T and postGain variants.
	q := float32(opts.Quality)
	mq := float32(opts.MaxQuality)
	T := -0.5 + 1.0*q/mq*0.5 // from -0.5 (standard CR) towards 0.0 (slightly sharper)
	post := 1.0 - 0.0015*q
	return &cubicKernel{T: T, postGain: post}
}

func (k *cubicKernel) SupportLeft() int  { return 1 }
func (k *cubicKernel) SupportRight() int { return 2 }
func (k *cubicKernel) TablePhases() int  { return 0 }

func (k *cubicKernel) Process(src []float32, startPos, ratio float64, dst []float32) {
	pos := startPos
	n := len(dst)
	L := len(src)
	for i := 0; i < n; i++ {
		x := pos
		j := int(math.Floor(x))
		t := float32(x - float64(j))

		jm1 := j - 1
		j0 := j
		j1 := j + 1
		j2 := j + 2
		if jm1 < 0 {
			jm1 = 0
		}
		if j0 < 0 {
			j0 = 0
		}
		if j1 >= L {
			j1 = L - 1
		}
		if j2 >= L {
			j2 = L - 1
		}
		y0 := src[jm1]
		y1 := src[j0]
		y2 := src[j1]
		y3 := src[j2]
		// Catmull-Rom Hermite form with adjustable tension T
		// m1 = (1-T)/2 * (y2 - y0)
		// m2 = (1-T)/2 * (y3 - y1)
		alpha := 0.5 * (1.0 - k.T)
		m1 := alpha * (y2 - y0)
		m2 := alpha * (y3 - y1)
		t2 := t * t
		t3 := t2 * t
		h00 := 2*t3 - 3*t2 + 1
		h10 := t3 - 2*t2 + t
		h01 := -2*t3 + 3*t2
		h11 := t3 - t2
		y := h00*y1 + h10*m1 + h01*y2 + h11*m2
		dst[i] = y * k.postGain
		pos += ratio
	}
}
