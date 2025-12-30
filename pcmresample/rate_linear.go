package pcmresample

import "math"

// rateKernel is implemented by all interpolation methods.
type rateKernel interface {
	SupportLeft() int
	SupportRight() int
	TablePhases() int // 0 if not applicable (linear/cubic)
	// Process: given src (planar single channel), startPos (fractional), ratio (src/dst),
	// and output buffer dst (length N), fill dst with resampled values.
	Process(src []float32, startPos, ratio float64, dst []float32)
}

type linearKernel struct {
	//Nothing to save in a linear kernel for now
}

func newLinearKernel() rateKernel {
	return &linearKernel{}
}

func (k *linearKernel) SupportLeft() int  { return 0 }
func (k *linearKernel) SupportRight() int { return 1 }
func (k *linearKernel) TablePhases() int  { return 0 }

func (k *linearKernel) Process(src []float32, startPos, ratio float64, dst []float32) {
	pos := startPos
	n := len(dst)
	L := len(src)
	for i := 0; i < n; i++ {
		x := pos
		j := int(math.Floor(x))
		t := float32(x - float64(j))
		j1 := j + 1
		if j < 0 {
			j = 0
		}
		if j1 >= L {
			j1 = L - 1
		}
		dst[i] = src[j]*(1-t) + src[j1]*t
		pos += ratio
	}
}
