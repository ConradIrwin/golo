package main

func main() {
	_, _ = c()

	panic("undefined: d")
}

func c() (int, int) {
	return 1, 2
}
