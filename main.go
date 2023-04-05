package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ConradIrwin/golo/golo"
)

func main() {
	flag.Usage = func() {
		fmt.Println("Usage: golo [-v] [test|run|build] [package|file]...")
		os.Exit(0)
	}
	vFlag := flag.Bool("v", false, "verbose")

	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
	}
	var mode = args[0]
	switch mode {
	case "run", "test", "build":
	default:
		flag.Usage()
	}

	runner := golo.New(mode, *vFlag, args[1:])

	if err := runner.Prepare(); err != nil {
		fmt.Println("golo: " + err.Error())
		os.Exit(1)
	}

	if exitStatus, err := runner.Run(); err != nil {
		fmt.Println("golo: " + err.Error())
		os.Exit(1)
	} else {
		os.Exit(exitStatus)
	}
}
