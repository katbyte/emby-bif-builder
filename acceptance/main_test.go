//go:build acceptance

// Package acceptance runs ebb as it ships, as a person runs it: the built program, given its settings in the environment and in a settings file,
// against a made-up Emby and Jellyfin. make testacc builds the program and runs these; EMBY_BIF_BUILDER_TEST_PROGRAM names a program built already.
package acceptance

import (
	"fmt"
	"os"
	"testing"
)

// programEnv names the program to run.
const programEnv = "EMBY_BIF_BUILDER_TEST_PROGRAM"

// program is the built ebb the tests run.
var program string

func TestMain(m *testing.M) {
	program = os.Getenv(programEnv)
	if program == "" {
		fmt.Fprintf(os.Stderr, "%s must name the program to run: \"make testacc\" builds one and runs these\n", programEnv)
		os.Exit(1)
	}
	if _, err := os.Stat(program); err != nil {
		fmt.Fprintf(os.Stderr, "%s names %s: %v\n", programEnv, program, err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}
