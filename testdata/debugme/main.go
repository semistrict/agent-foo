package main

import "fmt"

func add(a, b int) int {
	return a + b
}

func main() {
	x := 10
	y := 20
	z := add(x, y)
	fmt.Printf("Result: %d\n", z)
}
