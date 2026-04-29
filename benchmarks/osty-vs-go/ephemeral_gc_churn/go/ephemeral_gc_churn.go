package ephemeralgcchurnbench

import "strconv"

var keepAliveStrings []string

//go:noinline
func EphemeralGCChurn(n int) int64 {
	var total int64
	for i := 0; i < n; i++ {
		a := "key:" + strconv.Itoa(i%200000)
		b := a + ":" + strconv.Itoa((i*17)%97)
		xs := make([]string, 0, 3)
		xs = append(xs, a)
		xs = append(xs, b)
		xs = append(xs, "tail")
		total += int64(len(xs[0]) + len(xs[1]) + len(xs[2]) + len(xs))
		if i == n-1 {
			keepAliveStrings = xs
		}
	}
	return total
}
