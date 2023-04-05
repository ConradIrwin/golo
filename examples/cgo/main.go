package main

// int a() { return 1; }
import "C"
import "fmt"

func main() {
	fmt.Println(C.a())
}

type T struct {
	u any
}

func (*T) boop() {
	TODO this shoudl really boop better
}
