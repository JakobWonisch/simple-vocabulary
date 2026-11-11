package dictionary

import (
	"encoding/json"
	"strings"
)

// PosTags is the set of FreeDict part-of-speech tags for one lemma.
// JSON accepts either a string ("v") or an array (["n","v"]).
type PosTags []string

func (p *PosTags) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" || raw == `""` {
		*p = nil
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*p = nil
			return nil
		}
		*p = PosTags{s}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*p = normalizePosTags(list...)
	return nil
}

func (p PosTags) MarshalJSON() ([]byte, error) {
	if len(p) == 0 {
		return []byte("null"), nil
	}
	return json.Marshal([]string(p))
}

func (p PosTags) Has(tag string) bool {
	for _, t := range p {
		if t == tag {
			return true
		}
	}
	return false
}

// Primary prefers noun when present (articles/gender), otherwise the first tag.
func (p PosTags) Primary() string {
	if p.Has("n") {
		return "n"
	}
	if len(p) == 0 {
		return ""
	}
	return p[0]
}

// AllowsVerb is true when the lemma is untagged or explicitly verbal.
func (p PosTags) AllowsVerb() bool {
	return len(p) == 0 || p.Has("v")
}

func normalizePosTags(tags ...string) PosTags {
	out := make(PosTags, 0, len(tags))
	seen := map[string]struct{}{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
