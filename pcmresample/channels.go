package pcmresample

// mixMatrix: [dstCh][srcCh]
type mixMatrix struct {
	m [][]float32
}

// defaultMixMatrix builds a reasonable any-to-any matrix for 1,2,6,8 sources/targets,
// with energy-aware downmix and conservative upmix.
// Channel order (WAVEFORMATEXTENSIBLE):
// 5.1: FL, FR, FC, LFE, BL(or SL), BR(or SR)
// 7.1: FL, FR, FC, LFE, BL, BR, SL, SR
func defaultMixMatrix(srcCh, dstCh int, includeLFE bool) mixMatrix {
	M := make([][]float32, dstCh)
	for d := 0; d < dstCh; d++ {
		M[d] = make([]float32, srcCh)
	}
	// Identity when equal
	if srcCh == dstCh {
		for i := 0; i < srcCh; i++ {
			M[i][i] = 1
		}
		return mixMatrix{m: M}
	}

	// Helper getters for canonical roles in source
	type idxs struct{ L, R, C, LFE, SL, SR, BL, BR int }
	s := idxs{-1, -1, -1, -1, -1, -1, -1, -1}
	switch srcCh {
	case 1:
		// Mono as C
		s.C = 0
	case 2:
		s.L, s.R = 0, 1
	case 6:
		s.L, s.R, s.C, s.LFE, s.BL, s.BR = 0, 1, 2, 3, 4, 5
	case 8:
		s.L, s.R, s.C, s.LFE, s.BL, s.BR, s.SL, s.SR = 0, 1, 2, 3, 4, 5, 6, 7
	}

	// Compose downmix weights
	lfeW := float32(0.0)
	if includeLFE && s.LFE >= 0 {
		lfeW = 0.5 // -6 dB
	}

	// Build target roles
	switch dstCh {
	case 1: // mono
		row := M[0]
		add := func(i int, w float32) {
			if i >= 0 {
				row[i] += w
			}
		}
		// ITU-ish: L,R at 0.5, C at 0.707, surrounds at 0.5, LFE optional
		add(s.L, 0.5)
		add(s.R, 0.5)
		add(s.C, 0.707)
		add(s.SL, 0.5)
		add(s.SR, 0.5)
		add(s.BL, 0.5)
		add(s.BR, 0.5)
		add(s.LFE, lfeW)
		// Normalize to 1.0 gain
		sum := float32(0)
		for i := 0; i < srcCh; i++ {
			sum += row[i]
		}
		if sum > 1e-6 {
			for i := 0; i < srcCh; i++ {
				row[i] /= sum
			}
		}
	case 2: // stereo L/R
		// L' = L + 0.707*C + 0.707*Ls + 0.5*LFE (optional)
		// R' = R + 0.707*C + 0.707*Rs + 0.5*LFE (optional)
		addL := func(i int, w float32) {
			if i >= 0 {
				M[0][i] += w
			}
		}
		addR := func(i int, w float32) {
			if i >= 0 {
				M[1][i] += w
			}
		}
		if s.L >= 0 {
			addL(s.L, 1.0)
		}
		if s.R >= 0 {
			addR(s.R, 1.0)
		}
		if s.C >= 0 {
			addL(s.C, 0.707)
			addR(s.C, 0.707)
		}
		if s.SL >= 0 || s.BL >= 0 {
			avg := avgNonNeg(s.SL, s.BL)
			addL(avg, 0.707)
		}
		if s.SR >= 0 || s.BR >= 0 {
			avg := avgNonNeg(s.SR, s.BR)
			addR(avg, 0.707)
		}
		if s.LFE >= 0 && lfeW > 0 {
			addL(s.LFE, lfeW)
			addR(s.LFE, lfeW)
		}
		// gentle headroom scaling to avoid clipping when content is hot
		for d := 0; d < 2; d++ {
			sc := float32(0.9)
			for s := 0; s < srcCh; s++ {
				M[d][s] *= sc
			}
		}
	default:
		// Upmixing or arbitrary counts:
		// - Map existing L/R directly to nearest left/right positions.
		// - If C present in src, map it to center; else synthesize from (L+R)*0.707/2
		// - Surrounds: if src has surrounds, map them; else derive from L/R at -3 dB.
		// - LFE: passthrough if present, else zero.
		dstL, dstR, dstC, dstLFE := 0, min(1, dstCh-1), min(2, dstCh-1), min(3, dstCh-1)
		// Basic L/R
		if s.L >= 0 {
			M[dstL][s.L] += 1
		}
		if s.R >= 0 {
			M[dstR][s.R] += 1
		}
		// Center
		if dstC < dstCh {
			if s.C >= 0 {
				M[dstC][s.C] += 1
			} else if s.L >= 0 && s.R >= 0 {
				M[dstC][s.L] += 0.3535
				M[dstC][s.R] += 0.3535
			}
		}
		// LFE
		if dstLFE < dstCh && s.LFE >= 0 {
			M[dstLFE][s.LFE] += 1
		}
		// Distribute surrounds if available; else derive from L/R with -3 dB
		for d := 0; d < dstCh; d++ {
			// Map side/back pairs heuristically
			if d >= 4 { // positions beyond 5.1
				if d%2 == 0 && s.L >= 0 {
					M[d][s.L] += 0.707
				} else if d%2 == 1 && s.R >= 0 {
					M[d][s.R] += 0.707
				}
			}
		}
	}
	return mixMatrix{m: M}
}

func avgNonNeg(a, b int) int {
	if a >= 0 && b >= 0 {
		// choose the index which exists; both exist -> prefer side over back slightly
		return a
	}
	if a >= 0 {
		return a
	}
	if b >= 0 {
		return b
	}
	return -1
}
