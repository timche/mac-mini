package main

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// Shell-style globs rather than path.Match, whose `*` stops at a separator: the
// allowlist names whole paths like */OrbStack.app/* as well as bare command names.
// A regexp over bytes also settles the collation question outright — a character
// class here is the range it spells and nothing the locale would fold into it.
func globMatch(pattern, s string) bool {
	re, err := globRegexp(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`\A`)

	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			b.WriteString(`.*`)
		case '?':
			b.WriteString(`.`)
		case '[':
			end := closingBracket(pattern, i)
			if end < 0 {
				b.WriteString(regexp.QuoteMeta("["))
				continue
			}
			class := pattern[i+1 : end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i = end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}

	b.WriteString(`\z`)
	return regexp.Compile(b.String())
}

// A ] straight after the [ or after a leading ! is a literal, which is the one
// rule that keeps []] from reading as an empty class.
func closingBracket(pattern string, open int) int {
	i := open + 1
	if i < len(pattern) && pattern[i] == '!' {
		i++
	}
	if i < len(pattern) && pattern[i] == ']' {
		i++
	}
	for ; i < len(pattern); i++ {
		if pattern[i] == ']' {
			return i
		}
	}
	return -1
}

// One glob per line, `#` a reason. Matched against the command's name and against
// its full path, so the file can name `mds` or `/Applications/OrbStack.app/*`.
func readAllowlist(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

func allowed(patterns []string, path, name string) bool {
	for _, p := range patterns {
		if globMatch(p, name) || globMatch(p, path) {
			return true
		}
	}
	return false
}
