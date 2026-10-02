//go:build !android && !ios && !sqlite_android_test

package engine

// Desktop retains the existing pure-Go SQLite implementation and driver name.
import _ "modernc.org/sqlite"
