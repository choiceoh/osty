// markdown-stats — classify generated markdown-ish lines and emit
// per-type counts. Mirrors benchmarks/programs/markdown-stats/osty.

package main

import "fmt"

const N = 20000

var words = []string{
	"alpha", "beta", "gamma", "delta", "epsilon",
	"zeta", "eta", "theta", "iota", "kappa",
	"lambda", "mu", "nu", "xi", "omicron",
}

func generateLines(n int) []string {
	out := make([]string, n)
	state := int64(1)
	for i := 0; i < n; i++ {
		state = (state*1103515245 + 12345) % 2147483648
		kind := state % 100
		// Body composed of 3 words, deterministically.
		w1 := words[(state/2)%15]
		w2 := words[(state/8)%15]
		w3 := words[(state/32)%15]
		body := w1 + " " + w2 + " " + w3

		var line string
		if kind < 10 {
			line = "## " + body
		} else if kind < 20 {
			line = "# " + body
		} else if kind < 50 {
			line = "- " + body
		} else if kind < 60 {
			line = "> " + body
		} else if kind < 70 {
			line = ""
		} else {
			line = body
		}
		out[i] = line
	}
	return out
}

func main() {
	lines := generateLines(N)

	total := 0
	headings := 0
	subheadings := 0
	bullets := 0
	quotes := 0
	empty := 0
	plain := 0
	totalChars := 0
	headingChars := 0
	bulletChars := 0
	plainChars := 0

	for _, line := range lines {
		total++
		n := len(line)
		totalChars += n
		// Order matters: "## " must be checked before "# ".
		if n == 0 {
			empty++
		} else if len(line) >= 3 && line[:3] == "## " {
			subheadings++
			headingChars += n
		} else if len(line) >= 2 && line[:2] == "# " {
			headings++
			headingChars += n
		} else if len(line) >= 2 && line[:2] == "- " {
			bullets++
			bulletChars += n
		} else if len(line) >= 2 && line[:2] == "> " {
			quotes++
		} else {
			plain++
			plainChars += n
		}
	}

	fmt.Printf("total: %d\n", total)
	fmt.Printf("headings: %d\n", headings)
	fmt.Printf("subheadings: %d\n", subheadings)
	fmt.Printf("bullets: %d\n", bullets)
	fmt.Printf("quotes: %d\n", quotes)
	fmt.Printf("empty: %d\n", empty)
	fmt.Printf("plain: %d\n", plain)
	fmt.Printf("total_chars: %d\n", totalChars)
	fmt.Printf("heading_chars: %d\n", headingChars)
	fmt.Printf("bullet_chars: %d\n", bulletChars)
	fmt.Printf("plain_chars: %d\n", plainChars)
}
