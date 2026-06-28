package gobuster

import "testing"

// TestParseSample parses real gobuster output (banner + findings) and checks the
// header fields and the parsed Path/Status of each finding.
func TestParseSample(t *testing.T) {
	res, err := parseFile("testdata/sample.txt")
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if res.Target != "http://192.168.1.1" {
		t.Errorf("target = %q, want http://192.168.1.1", res.Target)
	}
	if res.Wordlist != "/usr/share/wordlists/dirb/common.txt" {
		t.Errorf("wordlist = %q", res.Wordlist)
	}
	want := map[string]int{
		"cgi-bin":    302,
		"cgi-bin/":   403,
		"index.html": 200,
		"webpages":   302,
	}
	if len(res.Findings) != len(want) {
		t.Fatalf("findings = %d, want %d", len(res.Findings), len(want))
	}
	for _, f := range res.Findings {
		ws, ok := want[f.Path]
		if !ok {
			t.Errorf("unexpected finding %q", f.Path)
			continue
		}
		if f.Status != ws {
			t.Errorf("finding %q status = %d, want %d", f.Path, f.Status, ws)
		}
	}
}
