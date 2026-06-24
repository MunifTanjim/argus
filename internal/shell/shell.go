package shell

import (
	"fmt"
	"os"
)

func StdErr(a ...any) {
	fmt.Fprint(os.Stderr, a...)
}

func StdErrF(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}

func StdErrLn(a ...any) {
	fmt.Fprintln(os.Stderr, a...)
}

func StdOut(a ...any) {
	fmt.Fprint(os.Stdout, a...)
}

func StdOutF(format string, a ...any) {
	fmt.Fprintf(os.Stdout, format, a...)
}

func StdOutLn(a ...any) {
	fmt.Fprintln(os.Stdout, a...)
}

func Exit(code int) {
	os.Exit(code)
}
