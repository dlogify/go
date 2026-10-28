// Package redact ports the engine's PII rules (services/engine/src/pipeline/
// redact.rs, docs/04 PII redaction) for client-side redaction (docs/12 §3.1
// step 3): same patterns, order and placeholders. The fixtures in
// clients/conformance/redaction keep the engine, the Node SDK and this
// package identical; the engine runs them too.
package redact

import (
	"net/netip"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const Secret = "<secret>"

// ws is Rust's \s (the White_Space property) as character class content:
// U+0085 is in, U+FEFF is not.
const ws = `\t\n\x0B\f\r \x{85}\x{A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}`

// isWord is Rust's \w: Alphabetic, marks, decimal digits, connector
// punctuation and Join_Control.
func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_Alphabetic, r) ||
		unicode.IsMark(r) || unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r) || r == 0x200C || r == 0x200D
}

// boundary is Rust's \b at byte offset i.
func boundary(s string, i int) bool {
	before, after := false, false
	if i > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:i])
		before = isWord(r)
	}
	if i < len(s) {
		r, _ := utf8.DecodeRuneInString(s[i:])
		after = isWord(r)
	}
	return before != after
}

func runeLen(s string, i int) int {
	if i >= len(s) {
		return 1
	}
	_, size := utf8.DecodeRuneInString(s[i:])
	return size
}

// A rule is tried at every start where Rust's \b holds and startOk accepts
// the character (a filter: it never rejects a start the pattern could
// match), with the pattern anchored there. Both engines use leftmost-first
// (Perl) semantics, so at a given start Go picks the same alternative as Rust.
//
// Cost stays linear in the input:
//   - run: when an attempt starting on a character of this class fails, every
//     later start in the same run fails too (the pattern's leading part must
//     reach the end of the run either way), so the search jumps past the run.
//   - shorter: when the trailing \b fails, Rust backtracks to a shorter match
//     at the same start; for these rules that is the longest end at a word
//     boundary.
type rule struct {
	re      *regexp.Regexp // ^(?:pattern), without its \b assertions
	startOk func(s string, i int) bool
	run     func(c byte) bool
	trail   func(sub []int) bool                 // whether this match needs \b after it
	shorter func(s string, start, end int) []int // nil: no shorter match exists
	replace func(s string, sub []int) string
}

func anchored(pattern string) *regexp.Regexp { return regexp.MustCompile(`^(?:` + pattern + `)`) }

func anyStart(string, int) bool { return true }

func always([]int) bool { return true }

func literal(text string) func(string, []int) string {
	return func(string, []int) string { return text }
}

func group(s string, sub []int, n int) string {
	if sub[2*n] < 0 {
		return ""
	}
	return s[sub[2*n]:sub[2*n+1]]
}

func (r *rule) match(s string, i int) []int {
	sub := r.re.FindStringSubmatchIndex(s[i:])
	if sub == nil {
		return nil
	}
	for k := range sub {
		if sub[k] >= 0 {
			sub[k] += i
		}
	}
	if r.trail != nil && r.trail(sub) && !boundary(s, sub[1]) {
		if r.shorter == nil {
			return nil
		}
		return r.shorter(s, sub[0], sub[1])
	}
	return sub
}

// replaceAll is Rust's replace_all for the rule, over the whole input.
func (r *rule) replaceAll(s string) string {
	var out strings.Builder
	last := 0
	for i := 0; i < len(s); {
		if !boundary(s, i) || !r.startOk(s, i) {
			i += runeLen(s, i)
			continue
		}
		sub := r.match(s, i)
		if sub == nil {
			next := i + runeLen(s, i)
			if r.run != nil && r.run(s[i]) {
				for next < len(s) && r.run(s[next]) {
					next++
				}
			}
			i = next
			continue
		}
		out.WriteString(s[last:i])
		out.WriteString(r.replace(s, sub))
		last = sub[1]
		if sub[1] > i {
			i = sub[1]
		} else {
			i += runeLen(s, i)
		}
	}
	if last == 0 {
		return s
	}
	out.WriteString(s[last:])
	return out.String()
}

// shorterByRegex tries every word-boundary end before end, longest first,
// with the whole pattern; only for rules whose matches are a few dozen bytes.
func shorterByRegex(full *regexp.Regexp) func(string, int, int) []int {
	return func(s string, start, end int) []int {
		for e := end - 1; e > start; e-- {
			if !utf8.RuneStart(s[e]) || !boundary(s, e) {
				continue
			}
			if sub := full.FindStringSubmatchIndex(s[start:e]); sub != nil {
				for k := range sub {
					if sub[k] >= 0 {
						sub[k] += start
					}
				}
				return sub
			}
		}
		return nil
	}
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// [A-Za-z0-9_\-]: JWT segments and the key of KEY_VALUE.
func isTokenByte(c byte) bool { return isASCIILetter(c) || isASCIIDigit(c) || c == '_' || c == '-' }

// [A-Za-z0-9._%+\-]: the local part of an email.
func isLocalByte(c byte) bool {
	return isASCIILetter(c) || isASCIIDigit(c) || c == '.' || c == '_' || c == '%' || c == '+' || c == '-'
}

// [a-z0-9+.\-], case-insensitive: a URL scheme.
func isSchemeByte(c byte) bool {
	return isASCIILetter(c) || isASCIIDigit(c) || c == '+' || c == '.' || c == '-'
}

// Case-insensitive classes also match U+017F and U+212A (multi-byte), so a
// start filter for them must let non-ASCII bytes through.
func letterOrNonASCII(s string, i int) bool { return isASCIILetter(s[i]) || s[i] >= utf8.RuneSelf }

func digitOrNonASCII(s string, i int) bool { return isASCIIDigit(s[i]) || s[i] >= utf8.RuneSelf }

// emailShorter finds the longest email ending before end on a word boundary:
// the domain is labels of [A-Za-z0-9-] separated by single dots (the regex
// already checked s[at+1:end]), and the match must end with a dot and at
// least two ASCII letters. One pass over the domain.
func emailShorter(s string, start, end int) []int {
	at := strings.IndexByte(s[start:end], '@') + start
	best := -1
	lastNonLetter := at // index of the last non-letter byte before the current end
	for e := at + 1; e < end; e++ {
		c := s[e-1]
		if !isASCIILetter(c) {
			lastNonLetter = e - 1
			continue
		}
		dot := lastNonLetter
		if s[dot] == '.' && dot > at+1 && e-dot-1 >= 2 && boundary(s, e) {
			best = e
		}
	}
	if best < 0 {
		return nil
	}
	return []int{start, best}
}

var (
	jwt = &rule{
		re:      anchored(`eyJ[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}\.[A-Za-z0-9_\-]{5,}`),
		startOk: func(s string, i int) bool { return strings.HasPrefix(s[i:], "eyJ") },
		run:     isTokenByte,
		replace: literal("<jwt>"),
	}
	bearer = &rule{
		re:      anchored(`(?i)(bearer)[` + ws + `]+[A-Za-z0-9\-._~+/]{8,}=*`),
		startOk: func(s string, i int) bool { return s[i] == 'b' || s[i] == 'B' },
		replace: func(s string, sub []int) string { return group(s, sub, 1) + " <token>" },
	}
	// The AKIA alternative is group 1: only it needs a \b after it.
	secretPrefixPattern = `(?:sk_live_|sk_test_|rk_live_|ghp_|gho_|ghs_|github_pat_|glpat-|xoxb-|xoxp-)[A-Za-z0-9_\-]{8,}|(AKIA[0-9A-Z]{16})|AIza[0-9A-Za-z_\-]{35}`
	secretPrefix        = &rule{
		re:      anchored(secretPrefixPattern),
		startOk: func(s string, i int) bool { return strings.IndexByte("srgxA", s[i]) >= 0 },
		trail:   func(sub []int) bool { return sub[2] >= 0 },
		replace: literal(Secret),
	}
	keyValue = &rule{
		re:      anchored(`(?i)([A-Za-z0-9_\-]*(?:password|passwd|secret|token|api_key|apikey|access_key))(["']?[` + ws + `]*[=:][` + ws + `]*["']?)([^` + ws + `"'&,;}]+)`),
		startOk: anyStart,
		run:     isTokenByte,
		replace: func(s string, sub []int) string { return group(s, sub, 1) + group(s, sub, 2) + Secret },
	}
	// scheme://user:password@ — the password becomes <secret>, the user is kept.
	urlUserinfo = &rule{
		re:      anchored(`(?i)([a-z][a-z0-9+.\-]*://)([^` + ws + `/@:]*):([^` + ws + `/@]+)@`),
		startOk: letterOrNonASCII,
		run:     isSchemeByte,
		replace: func(s string, sub []int) string { return group(s, sub, 1) + group(s, sub, 2) + ":" + Secret + "@" },
	}
	email = &rule{
		re:      anchored(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`),
		startOk: func(s string, i int) bool { return isLocalByte(s[i]) },
		run:     isLocalByte,
		trail:   always,
		shorter: emailShorter,
		replace: literal("<email>"),
	}
	cardPattern = `\p{Nd}(?:[ \-]?\p{Nd}){12,18}`
	card        = &rule{
		re:      anchored(cardPattern),
		startOk: digitOrNonASCII,
		trail:   always,
		shorter: shorterByRegex(regexp.MustCompile(`^(?:` + cardPattern + `)$`)),
		replace: func(s string, sub []int) string { return redactCardCandidate(s[sub[0]:sub[1]]) },
	}
	ipv4 = &rule{
		re:      anchored(`\p{Nd}{1,3}(?:\.\p{Nd}{1,3}){3}`),
		startOk: digitOrNonASCII,
		trail:   always,
		shorter: shorterByRegex(regexp.MustCompile(`^(?:\p{Nd}{1,3}(?:\.\p{Nd}{1,3}){3})$`)),
		replace: func(s string, sub []int) string {
			m := s[sub[0]:sub[1]]
			if addr, err := netip.ParseAddr(m); err == nil && addr.Is4() {
				return "<ip>"
			}
			return m
		},
	}
	digitGroup = regexp.MustCompile(`\p{Nd}+`)
	// The candidate must not follow a letter, digit, colon or dot; that it is
	// not followed by one is checked in code (as the engine does).
	ipv6 = regexp.MustCompile(`(?i)(^|[^0-9a-z:.])((?:[0-9a-f]{1,4})?(?::[0-9a-f]{0,4}){2,7})`)
)

// Text applies the engine's text rules in the engine's order.
func Text(s string) string {
	s = jwt.replaceAll(s)
	s = bearer.replaceAll(s)
	s = secretPrefix.replaceAll(s)
	s = keyValue.replaceAll(s)
	s = urlUserinfo.replaceAll(s)
	s = email.replaceAll(s)
	s = card.replaceAll(s)
	s = ipv4.replaceAll(s)
	return redactIPv6(s)
}

// The candidate pattern is greedy, so a card next to another number
// ("4111… 12/28") arrives as one run that fails Luhn. Then try every run of
// whole digit groups; a group's length is counted in UTF-8 bytes, as the
// engine does.
func redactCardCandidate(c string) string {
	if luhn(c) {
		return "<card>"
	}
	groups := digitGroup.FindAllStringIndex(c, -1)
	for i := range groups {
		for j := len(groups) - 1; j >= i; j-- {
			digits := 0
			for k := i; k <= j; k++ {
				digits += groups[k][1] - groups[k][0]
			}
			start, end := groups[i][0], groups[j][1]
			if digits >= 13 && digits <= 19 && luhn(c[start:end]) {
				return redactCardCandidate(c[:start]) + "<card>" + redactCardCandidate(c[end:])
			}
		}
	}
	return c
}

// Only ASCII digits count, like Rust's char::to_digit(10).
func luhn(c string) bool {
	var digits []int
	for i := 0; i < len(c); i++ {
		if c[i] >= '0' && c[i] <= '9' {
			digits = append(digits, int(c[i]-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	for i := 0; i < len(digits); i++ {
		d := digits[len(digits)-1-i]
		if i%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

func isASCIIAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// `db::add` and `a[::2]` parse as IPv6 too. Real addresses in logs have at
// least three groups and a digit, or are the loopback.
func looksLikeAnAddress(c string) bool {
	groups := 0
	for _, g := range strings.Split(c, ":") {
		if g != "" {
			groups++
		}
	}
	return c == "::1" || groups >= 3 && strings.ContainsAny(c, "0123456789")
}

func redactIPv6(s string) string {
	var out strings.Builder
	last := 0
	for _, m := range ipv6.FindAllStringSubmatchIndex(s, -1) {
		start, end := m[4], m[5]
		next := end >= len(s) || !(isASCIIAlnum(s[end]) || s[end] == ':' || s[end] == '.')
		candidate := s[start:end]
		if !next || !looksLikeAnAddress(candidate) {
			continue
		}
		if addr, err := netip.ParseAddr(candidate); err != nil || !addr.Is6() || addr.Zone() != "" {
			continue
		}
		out.WriteString(s[last:start])
		out.WriteString("<ip>")
		last = end
	}
	if last == 0 {
		return s
	}
	out.WriteString(s[last:])
	return out.String()
}

var sensitiveKeys = []string{"password", "passwd", "secret", "token", "authorization", "cookie", "api_key", "api-key", "apikey", "session"}

// IsSensitiveKey: the key, lower-cased in ASCII, contains a sensitive word.
func IsSensitiveKey(key string) bool {
	lower := ASCIILower(key)
	for _, k := range sensitiveKeys {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

func ASCIILower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
