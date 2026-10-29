// Package collate orders names the way `sort` orders them in a session on this Mac, whose
// locale is en_US.UTF-8.
//
// It is here because the listing this binary prints replaced a `find | sort` pipeline, and
// that order is nothing like a byte order: `sort` compares case-folded first, so
// `README.md` lands between `prs.md` and `secrets.md` rather than ahead of every lowercase
// name, and the punctuation a name is broken up with has weights of its own, which put
// `mac-mini.md` before `mac.md`. A Go sort of the same names would reshuffle the listing
// every session reads, which is a diff in the context of every session for no reason.
//
// Three levels, the first two of the collation and the third `sort`'s own last resort,
// since a name is sorted whole: the case-folded weights below, then case with lowercase
// first, then the bytes.
package collate

import (
	"strings"
	"unicode"
)

// The primary weights, read off `sort` rather than assumed: every printable ASCII
// character sorted on its own, with the pairs that differ in case alone folded together.
// Punctuation before digits before letters, and within punctuation an order of its own
// that is not ASCII's — `_` and `-` come before `.`, which is why `mac-mini.md` precedes
// `mac.md`.
const weights = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789abcdefghijklmnopqrstuvwxyz"

// A character the table has no weight for — an accented letter, another script — sorts
// after every name made of the ones it has, where the collation would have folded it onto
// a base letter. Carrying a copy of the Unicode collation table to place it is not worth
// what it buys: no docs folder has such a name, and an order that is merely stable is
// still a listing.
func primary(r rune) int {
	folded := unicode.ToLower(r)
	if i := strings.IndexRune(weights, folded); i >= 0 {
		return i
	}
	return len(weights) + int(folded)
}

// Compare returns a negative number when a sorts before b, a positive one when it sorts
// after, and zero when `sort` would have left the two in the order they came.
func Compare(a, b string) int {
	if c := compare(a, b, primary); c != 0 {
		return c
	}
	if c := compare(a, b, upper); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func upper(r rune) int {
	if unicode.IsUpper(r) {
		return 1
	}
	return 0
}

// A shorter name wins a comparison its every character tied, which is what running out of
// weights stands for here.
func compare(a, b string, weight func(rune) int) int {
	left, right := []rune(a), []rune(b)
	for i := 0; i < len(left) && i < len(right); i++ {
		x, y := weight(left[i]), weight(right[i])
		if x != y {
			return x - y
		}
	}
	return len(left) - len(right)
}
