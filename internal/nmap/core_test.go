package nmap

import "testing"

// TestParseSample parses a real nmap -oX capture and checks the compact result.
func TestParseSample(t *testing.T) {
	res, err := parseFile("testdata/sample.xml")
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(res.Hosts) != 1 {
		t.Fatalf("hosts = %d, want 1", len(res.Hosts))
	}
	h := res.Hosts[0]
	if h.Addr != "192.168.1.112" {
		t.Errorf("addr = %q, want 192.168.1.112", h.Addr)
	}
	if !h.Up {
		t.Error("host should be up")
	}
	if h.Closed != 984 {
		t.Errorf("closed count = %d, want 984", h.Closed)
	}

	want := map[int]struct {
		state, svc string
	}{
		22:    {"open", "ssh"},
		80:    {"filtered", "http"},
		111:   {"open", "rpcbind"},
		843:   {"open", "-"}, // no <service> in the XML → "-"
		10000: {"filtered", "snet-sensor-mgmt"},
	}
	got := map[int]Port{}
	for _, p := range h.Ports {
		got[p.Num] = p
	}
	for num, w := range want {
		p, ok := got[num]
		if !ok {
			t.Errorf("port %d missing from result", num)
			continue
		}
		if p.State != w.state || p.Service != w.svc {
			t.Errorf("port %d = {%s %s}, want {%s %s}", num, p.State, p.Service, w.state, w.svc)
		}
	}
}

// TestParsePingSweep guards the multi-host path: a -sn sweep must yield ALL
// hosts (regression — the parser once kept only the first), each up and
// port-less.
func TestParsePingSweep(t *testing.T) {
	res, err := parseFile("testdata/pingsweep.xml")
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(res.Hosts) != 3 {
		t.Fatalf("hosts = %d, want 3", len(res.Hosts))
	}
	wantAddr := []string{"192.168.1.1", "192.168.1.50", "192.168.1.112"}
	for i, h := range res.Hosts {
		if h.Addr != wantAddr[i] {
			t.Errorf("host[%d].Addr = %q, want %q", i, h.Addr, wantAddr[i])
		}
		if !h.Up {
			t.Errorf("host[%d] should be up", i)
		}
		if len(h.Ports) != 0 {
			t.Errorf("host[%d] should have no ports, got %d", i, len(h.Ports))
		}
	}
}
