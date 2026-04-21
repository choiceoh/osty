package fibbench

//go:noinline
func Fib(n int64) int64 {
	if n < 2 {
		return n
	}
	return Fib(n-1) + Fib(n-2)
}
