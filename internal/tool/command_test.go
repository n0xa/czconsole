package tool

import (
	"reflect"
	"testing"
)

func TestSubstitute(t *testing.T) {
	const out = "/home/kali/nmap/nmap-ts"
	cases := []struct {
		name string
		argv []string
		vals map[string]string
		want []string
	}{
		{
			name: "nmap opts word-split",
			argv: []string{"nmap", "--privileged", "-oA", "{{outfile}}", "{{opts...}}"},
			vals: map[string]string{"opts": "-sS -p 80,443 192.168.1.0/24"},
			want: []string{"nmap", "--privileged", "-oA", out, "-sS", "-p", "80,443", "192.168.1.0/24"},
		},
		{
			name: "gobuster single-value tokens",
			argv: []string{"gobuster", "dir", "-u", "{{url}}", "-w", "{{wordlist}}", "{{opts...}}"},
			vals: map[string]string{"url": "http://x", "wordlist": "/wl", "opts": "-t 50"},
			want: []string{"gobuster", "dir", "-u", "http://x", "-w", "/wl", "-t", "50"},
		},
		{
			name: "empty split token drops",
			argv: []string{"nmap", "{{opts...}}"},
			vals: map[string]string{"opts": ""},
			want: []string{"nmap"},
		},
		{
			name: "inline mix (outfile suffix)",
			argv: []string{"tcpdump", "-w", "{{outfile}}.pcap"},
			vals: map[string]string{},
			want: []string{"tcpdump", "-w", out + ".pcap"},
		},
		{
			name: "injection is inert (one literal arg)",
			argv: []string{"sh", "{{url}}"},
			vals: map[string]string{"url": "; rm -rf /"},
			want: []string{"sh", "; rm -rf /"}, // a single literal arg, not a command
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Substitute(c.argv, c.vals, out)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Substitute = %q\n              want %q", got, c.want)
			}
		})
	}
}
