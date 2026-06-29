package tool

import "testing"

// TestLoadShippedSpecs parses the real packaged specs to keep the JSON and the
// Go schema in lockstep.
func TestLoadShippedSpecs(t *testing.T) {
	specs, err := Load("../../packaging/tools.d")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	byID := map[string]Spec{}
	for _, s := range specs {
		byID[s.ID] = s
	}

	nmap, ok := byID["nmap"]
	if !ok {
		t.Fatal("nmap spec missing")
	}
	if nmap.Class != ClassNetRaw {
		t.Errorf("nmap class = %q, want %q", nmap.Class, ClassNetRaw)
	}
	if len(nmap.Inputs) < 1 || nmap.Inputs[0].ID != "opts" {
		t.Errorf("nmap inputs = %+v", nmap.Inputs)
	}
	// the syntax-hint note (not a real field) renders but collects no value
	if nmap.Inputs[len(nmap.Inputs)-1].Type != "note" {
		t.Errorf("nmap last input should be a note, got %+v", nmap.Inputs[len(nmap.Inputs)-1])
	}
	if nmap.Results.Kind != "text" || nmap.Results.File != ".nmap" || nmap.Results.StripPrefix != "#" {
		t.Errorf("nmap results = %+v", nmap.Results)
	}
	if nmap.Running.Controls != ControlsCancelBg {
		t.Errorf("nmap controls = %q", nmap.Running.Controls)
	}

	gob, ok := byID["gobuster"]
	if !ok {
		t.Fatal("gobuster spec missing")
	}
	if gob.Class != ClassPlain {
		t.Errorf("gobuster class = %q, want %q", gob.Class, ClassPlain)
	}
	if len(gob.Inputs) != 3 {
		t.Errorf("gobuster inputs = %d, want 3", len(gob.Inputs))
	}
	if wl, _ := gob.Input("wordlist"); wl.Default != "/usr/share/wordlists/dirb/common.txt" {
		t.Errorf("gobuster wordlist default = %q", wl.Default)
	}

	td, ok := byID["tcpdump"]
	if !ok {
		t.Fatal("tcpdump spec missing")
	}
	if td.Class != ClassNetRaw {
		t.Errorf("tcpdump class = %q, want %q", td.Class, ClassNetRaw)
	}
	if td.Results.Kind != "path" {
		t.Errorf("tcpdump results kind = %q, want path", td.Results.Kind)
	}
	if td.Running.Controls != ControlsStop {
		t.Errorf("tcpdump controls = %q, want %q", td.Running.Controls, ControlsStop)
	}

	if _, ok := byID["rtl_433"]; !ok {
		t.Error("rtl_433 spec missing")
	}
	rp, ok := byID["rtl_power"]
	if !ok {
		t.Fatal("rtl_power spec missing")
	}
	if len(rp.Inputs) != 8 {
		t.Errorf("rtl_power inputs = %d, want 8", len(rp.Inputs))
	}
	if rp.Post == nil || rp.Post.When != "heatmap" {
		t.Errorf("rtl_power post = %+v, want gated on the heatmap input", rp.Post)
	}
}
