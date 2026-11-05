package main

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
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
		Dir: "testdata/script",
		Cmds: map[string]func(*testscript.TestScript, bool, []string){
			"exitcode": exitCode,
			"capture":  capture,
			"expand":   expand,
		},
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

// capture runs `capture VAR REGEXP` and sets VAR to the first group of REGEXP in the last stdout.
func capture(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: capture VAR REGEXP")
	}
	re, err := regexp.Compile(args[1])
	ts.Check(err)
	m := re.FindStringSubmatch(ts.ReadFile("stdout"))
	if len(m) < 2 {
		ts.Fatalf("stdout has no group of %q", args[1])
	}
	ts.Setenv(args[0], m[1])
}

// expand runs `expand SRC DST` and writes SRC to DST with $VAR replaced from the script environment.
func expand(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: expand SRC DST")
	}
	ts.Check(os.WriteFile(ts.MkAbs(args[1]), []byte(os.Expand(ts.ReadFile(args[0]), ts.Getenv)), 0o600))
}
