// Package domain holds the rules of watching: what a change is, which
// changes wake, how a wake reads and who holds a watch.
package domain

// Protocol is the wake contract's number. Changing a field, a kind or the
// wake's shape is a new protocol; see docs/reference/wake-contract.md.
const Protocol = 1
