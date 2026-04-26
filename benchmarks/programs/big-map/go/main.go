package main

import "fmt"
import "strconv"

func main() {
    n := 200000
    lookups := 1000000

    m := make(map[string]int, n)
    for i := 0; i < n; i++ {
        m["key:"+strconv.Itoa(i)] = i
    }

    state := 1
    sum := 0
    hits := 0
    for j := 0; j < lookups; j++ {
        state = (state*1103515245 + 12345) % 2147483648
        target := state % n
        key := "key:" + strconv.Itoa(target)
        if _, ok := m[key]; ok {
            hits++
            sum += state % 7
        }
    }

    fmt.Println("size:", len(m))
    fmt.Println("hits:", hits)
    fmt.Println("sum:", sum)
}
