package main

func main() {
	a, b := c()

	d(a, b)
}

func c() (int, int) {
	return 1, 2
}
