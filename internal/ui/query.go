package ui

import "strings"

// GitHub-style filter terms in the in-app filter. The list is already
// loaded, so these match client-side: no refetch, and they narrow whatever
// the server-side github.filter returned rather than replacing it.
//
// A bare word stays a fuzzy match across the enabled fields, which is what
// the filter always did. Only a "key:value" term (optionally negated with
// a leading "-") is treated as a qualifier.

// Term is one parsed filter term.
type Term struct {
	// Key is the qualifier name ("label", "author", "is"), empty for a
	// bare word.
	Key string
	// Value is what to match. For a bare word, the word itself.
	Value string
	// Negated inverts the term: "-label:deps" keeps rows without it.
	Negated bool
}

// Bare reports a term with no qualifier, matched fuzzily across fields.
func (t Term) Bare() bool { return t.Key == "" }

// ParseQuery splits a filter string into terms. Quoted values keep their
// spaces ("label:\"needs review\""), so a multi-word label is one term.
func ParseQuery(q string) []Term {
	var out []Term
	for _, tok := range tokenize(q) {
		t := Term{Value: tok}
		if strings.HasPrefix(t.Value, "-") && len(t.Value) > 1 {
			t.Negated, t.Value = true, t.Value[1:]
		}
		// A colon makes it a qualifier, but only with something either
		// side: "foo:" or ":bar" is more likely a typo than a term, and
		// treating it as one would silently match nothing.
		if k, v, ok := strings.Cut(t.Value, ":"); ok && k != "" && v != "" {
			t.Key, t.Value = strings.ToLower(k), v
		} else if t.Negated {
			// A negated bare word ("-renovate") still excludes.
			t.Key = ""
		}
		out = append(out, t)
	}
	return out
}

// tokenize splits on spaces, honouring double quotes so a value can hold
// one.
func tokenize(q string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range q {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// HasQualifier reports whether a query uses any key:value term, so a view
// can tell a GitHub-style filter from a plain fuzzy one.
func HasQualifier(q string) bool {
	for _, t := range ParseQuery(q) {
		if !t.Bare() {
			return true
		}
	}
	return false
}
