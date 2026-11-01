package main

import (
	"strconv"
	"time"
)

// Busy timeouts for the two kinds of process that open the database file.
const (
	// serverBusyTimeout bounds how long a request waits behind another
	// process's write before it fails: a longer wait is better shown as an
	// error than as a hung request.
	serverBusyTimeout = 5 * time.Second
	// maintenanceBusyTimeout is for the maintenance subcommands, which run
	// against the live file and may wait longer for the server's writes.
	maintenanceBusyTimeout = 10 * time.Second
)

// sqliteDSN is how smeldr-server and its maintenance subcommands open the
// SQLite file (T1013): WAL, so readers never block the writer; busy_timeout,
// so a writer waits up to busy for another process's write lock instead of
// failing at once; and _txlock=immediate, so a transaction takes the write
// lock at BEGIN and cannot fail mid-way on a stale WAL snapshot.
func sqliteDSN(path string, busy time.Duration) string {
	return path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(" + strconv.FormatInt(busy.Milliseconds(), 10) + ")&_txlock=immediate"
}
