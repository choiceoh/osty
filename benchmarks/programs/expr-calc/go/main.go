// expr-calc — tiny arithmetic interpreter.
//
// Generates 5,000 fixed-shape expressions (5 single-digit operands
// joined by + - *), tokenizes each, runs shunting-yard to produce
// postfix, then evaluates on an Int stack. Stats: total, min, max,
// sum, count by sign.

package main

import (
	"fmt"
	"strings"
)

const N = 5000

var ops = []string{"+", "-", "*"}
var nums = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}

func buildExpressions(k int) []string {
	out := make([]string, k)
	state := int64(1)
	pickNum := func() string {
		state = (state*1103515245 + 12345) % 2147483648
		return nums[(state/4)%9]
	}
	pickOp := func() string {
		state = (state*1103515245 + 12345) % 2147483648
		return ops[(state/4)%3]
	}
	for i := 0; i < k; i++ {
		a := pickNum()
		o1 := pickOp()
		b := pickNum()
		o2 := pickOp()
		c := pickNum()
		o3 := pickOp()
		d := pickNum()
		o4 := pickOp()
		e := pickNum()
		out[i] = a + " " + o1 + " " + b + " " + o2 + " " + c + " " + o3 + " " + d + " " + o4 + " " + e
	}
	return out
}

func isOp(t string) bool {
	return t == "+" || t == "-" || t == "*"
}

func opPrec(op string) int {
	if op == "*" {
		return 2
	}
	return 1
}

func shuntingYard(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	stack := make([]string, 0, 8)
	for _, t := range tokens {
		if isOp(t) {
			for len(stack) > 0 && opPrec(stack[len(stack)-1]) >= opPrec(t) {
				out = append(out, stack[len(stack)-1])
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, t)
		} else {
			out = append(out, t)
		}
	}
	for len(stack) > 0 {
		out = append(out, stack[len(stack)-1])
		stack = stack[:len(stack)-1]
	}
	return out
}

func toInt(s string) int64 {
	switch s {
	case "1":
		return 1
	case "2":
		return 2
	case "3":
		return 3
	case "4":
		return 4
	case "5":
		return 5
	case "6":
		return 6
	case "7":
		return 7
	case "8":
		return 8
	case "9":
		return 9
	}
	return 0
}

func evalPostfix(postfix []string) int64 {
	stack := make([]int64, 0, 8)
	for _, t := range postfix {
		if isOp(t) {
			b := stack[len(stack)-1]
			a := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			var r int64
			switch t {
			case "+":
				r = a + b
			case "-":
				r = a - b
			case "*":
				r = a * b
			}
			stack = append(stack, r)
		} else {
			stack = append(stack, toInt(t))
		}
	}
	return stack[0]
}

func main() {
	exprs := buildExpressions(N)

	total := 0
	var sum int64
	min := int64(1 << 62)
	max := int64(-(1 << 62))
	pos := 0
	neg := 0
	zero := 0

	for _, expr := range exprs {
		tokens := strings.Split(expr, " ")
		postfix := shuntingYard(tokens)
		v := evalPostfix(postfix)
		total++
		sum += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		if v > 0 {
			pos++
		} else if v < 0 {
			neg++
		} else {
			zero++
		}
	}

	fmt.Printf("expressions: %d\n", total)
	fmt.Printf("sum: %d\n", sum)
	fmt.Printf("min: %d\n", min)
	fmt.Printf("max: %d\n", max)
	fmt.Printf("positive: %d\n", pos)
	fmt.Printf("negative: %d\n", neg)
	fmt.Printf("zero: %d\n", zero)
}
