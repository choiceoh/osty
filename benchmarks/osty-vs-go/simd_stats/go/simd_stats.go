package simdstatsbench

func buildSIMDData(n int) ([]int, []int) {
	values := make([]int, n)
	weights := make([]int, n)
	for i := 0; i < n; i++ {
		values[i] = ((i * 17) % 97) - 45 + (i % 7)
		weights[i] = ((i * 29) % 89) + 25
	}
	return values, weights
}

var simdValues, simdWeights = buildSIMDData(2048)

//go:noinline
func AnalyzeSimdStats(values, weights []int) int {
	n := len(values)
	if len(weights) < n {
		n = len(weights)
	}

	dot := 0
	energy := 0
	peak := -1 << 30
	hits := 0

	for i := 0; i < n; i++ {
		mixed := values[i]*weights[i] + values[i]*4 - weights[i]
		dot += mixed
		energy += mixed * mixed
		if mixed > 1500 {
			hits++
		}
	}

	for i := 0; i+7 < n; i++ {
		window := values[i]*weights[i]*3 +
			values[i+1]*weights[i+1]*5 +
			values[i+2]*weights[i+2]*7 +
			values[i+3]*weights[i+3]*11 +
			values[i+4]*weights[i+4]*13 +
			values[i+5]*weights[i+5]*17 +
			values[i+6]*weights[i+6]*19 +
			values[i+7]*weights[i+7]*23
		if window > peak {
			peak = window
		}
	}

	out := dot + energy/97 + hits*31 + peak/11
	if out < 0 {
		out = -out
	}
	return out
}
