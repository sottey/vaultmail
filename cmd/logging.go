package cmd

import "fmt"

func verbosef(format string, args ...interface{}) {
	if !verbose {
		return
	}
	fmt.Printf(format, args...)
}
