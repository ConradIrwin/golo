package main

import "fmt"

func main() {
	c()
	panic("string literal not terminated")
}

func c() {
	fmt.Println("🧟")
}