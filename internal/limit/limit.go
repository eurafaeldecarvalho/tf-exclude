// Package limit splits a terraform argument list so it fits in one process
// command line. Terraform has no -target-file, and Windows CreateProcess stops
// at 32767 characters, so a few thousand -target flags have to be run as
// successive terraform invocations.
package limit

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Default is a conservative command-line budget in bytes, below the OS limit
// so the terraform path and the environment still fit.
func Default() int {
	if v := strings.TrimSpace(os.Getenv("TF_EXCLUDE_ARG_LIMIT")); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	if runtime.GOOS == "windows" {
		// CreateProcess limit is 32767 characters. Leave room for the
		// executable path and quoting.
		return 30000
	}
	// Linux is typically 2 MiB and current macOS is at least 256 KiB.
	// 200 KiB stays inside the smaller of those without asking the OS.
	return 200000
}

// Split divides targets into batches. Each batch is base followed by
// "-target=<addr>" arguments, and each batch's argv length is <= budget.
// argv0 is the terraform executable path and is counted in every batch.
func Split(argv0 string, base, targets []string, budget int) ([][]string, error) {
	if budget <= 0 {
		return nil, fmt.Errorf("invalid command-line budget %d", budget)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no targets to pass")
	}
	var batches [][]string
	var cur []string
	curLen := argvLen(argv0, base)
	if curLen >= budget {
		return nil, fmt.Errorf("terraform command is %d bytes before any -target flag, over the %d byte limit", curLen, budget)
	}
	flush := func() {
		if len(cur) == 0 {
			return
		}
		batch := make([]string, 0, len(base)+len(cur))
		batch = append(batch, base...)
		batch = append(batch, cur...)
		batches = append(batches, batch)
		cur = nil
		curLen = argvLen(argv0, base)
	}
	for _, addr := range targets {
		flag := "-target=" + addr
		cost := argCost(flag)
		if argvLen(argv0, base)+cost > budget {
			return nil, fmt.Errorf("single -target flag for %q is %d bytes and does not fit in the %d byte limit", addr, argvLen(argv0, append(append([]string{}, base...), flag)), budget)
		}
		if curLen+cost > budget {
			flush()
		}
		cur = append(cur, flag)
		curLen += cost
	}
	flush()
	return batches, nil
}

func argvLen(argv0 string, args []string) int {
	n := argCost(argv0)
	for _, a := range args {
		n += argCost(a)
	}
	return n
}

func argCost(a string) int {
	// Trailing separator, plus a pair of quotes when the OS would need them.
	n := len(a) + 1
	if strings.ContainsAny(a, " \t\"") {
		n += 2
	}
	return n
}
