package main

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"plait": func() { os.Exit(run()) },
	})
}

func TestScript(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:  "testdata/script",
		Cmds: map[string]func(*testscript.TestScript, bool, []string){"exitcode": exitCode},
	})
}

// exitCode runs `exitcode N cmd args...` and fails unless cmd exits with N; testscript's exec only tells zero from non-zero.
func exitCode(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("exitcode does not support negation")
	}
	if len(args) < 2 {
		ts.Fatalf("usage: exitcode N command [args...]")
	}
	want, err := strconv.Atoi(args[0])
	ts.Check(err)
	got := 0
	if execErr := ts.Exec(args[1], args[2:]...); execErr != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](execErr)
		if !ok {
			ts.Fatalf("run %s: %v", args[1], execErr)
		}
		got = exitErr.ExitCode()
	}
	if got != want {
		ts.Fatalf("exit code = %d, want %d", got, want)
	}
}
