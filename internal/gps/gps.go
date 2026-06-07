// Package gps reads a one-shot fix snapshot from a running gpsd. It is
// best-effort and fast-failing: if gpsd isn't listening (e.g. stock RaspiOS
// without the LoRa+GNSS cap configured), Poll returns a zero Fix with no error
// so the dashboard simply shows "no GPS" rather than stalling.
package gps

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"
	"time"
)

type Fix struct {
	Mode int     `json:"mode"`  // 0/1 = no fix, 2 = 2D, 3 = 3D
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Sats int     `json:"sats"`  // satellites used, when SKY is seen
	OK   bool    `json:"ok"`    // true once we have a mode>=2
}

// Sat is one satellite from a gpsd SKY report.
type Sat struct {
	PRN  int     `json:"prn"`
	El   float64 `json:"el"`   // elevation, degrees
	Az   float64 `json:"az"`   // azimuth, degrees
	SNR  float64 `json:"snr"`  // signal, dB-Hz
	Used bool    `json:"used"`
}

// Detail is the full cgps-style snapshot: a complete TPV plus the SKY sat list.
type Detail struct {
	Mode  int     `json:"mode"`
	Time  string  `json:"time"`
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	Alt   float64 `json:"alt"`   // MSL, meters
	Speed float64 `json:"speed"` // m/s
	Track float64 `json:"track"` // deg
	Climb float64 `json:"climb"` // m/s
	Eph   float64 `json:"eph"`   // horizontal error estimate, m
	Epv   float64 `json:"epv"`   // vertical error estimate, m
	Used  int     `json:"used"`
	Seen  int     `json:"seen"`
	Sats  []Sat   `json:"sats"`
	OK    bool    `json:"ok"`
}

// PollDetail collects one TPV and one SKY (or times out), for the GPS modal.
func PollDetail(timeout time.Duration) Detail {
	var d Detail
	conn, err := net.DialTimeout("tcp", "127.0.0.1:2947", 250*time.Millisecond)
	if err != nil {
		return d
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte("?WATCH={\"enable\":true,\"json\":true}\n")); err != nil {
		return d
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	gotTPV, gotSKY := false, false
	for sc.Scan() {
		var m struct {
			Class  string  `json:"class"`
			Mode   int     `json:"mode"`
			Time   string  `json:"time"`
			Lat    float64 `json:"lat"`
			Lon    float64 `json:"lon"`
			AltMSL float64 `json:"altMSL"`
			Alt    float64 `json:"alt"`
			AltHAE float64 `json:"altHAE"`
			Speed  float64 `json:"speed"`
			Track  float64 `json:"track"`
			Climb  float64 `json:"climb"`
			Eph    float64 `json:"eph"`
			Epx    float64 `json:"epx"`
			Epv    float64 `json:"epv"`
			Sats   []struct {
				PRN  int     `json:"PRN"`
				El   float64 `json:"el"`
				Az   float64 `json:"az"`
				SS   float64 `json:"ss"`
				Used bool    `json:"used"`
			} `json:"satellites"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Class {
		case "TPV":
			d.Mode, d.Time, d.Lat, d.Lon = m.Mode, m.Time, m.Lat, m.Lon
			d.Alt = firstNonZero(m.AltMSL, m.Alt, m.AltHAE)
			d.Speed, d.Track, d.Climb = m.Speed, m.Track, m.Climb
			d.Eph = firstNonZero(m.Eph, m.Epx)
			d.Epv = m.Epv
			d.OK = m.Mode >= 2
			gotTPV = true
		case "SKY":
			d.Sats = nil
			d.Used, d.Seen = 0, 0
			for _, s := range m.Sats {
				d.Sats = append(d.Sats, Sat{PRN: s.PRN, El: s.El, Az: s.Az, SNR: s.SS, Used: s.Used})
				d.Seen++
				if s.Used {
					d.Used++
				}
			}
			gotSKY = true
		}
		if gotTPV && gotSKY {
			break
		}
	}
	return d
}

func firstNonZero(vals ...float64) float64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

var (
	lastMu   sync.Mutex
	lastFix  Fix
	lastSeen time.Time
)

// Poll returns a current fix, smoothing transient misses. gpsd emits ~1 Hz, and
// when another client (kismet, during wardrive) is also subscribed, a single
// short read can miss the TPV window. If a fresh read finds no fix but we had
// one within the last few seconds, hold it so the UI doesn't flap to "no fix".
func Poll(timeout time.Duration) Fix {
	f := pollOnce(timeout)
	lastMu.Lock()
	defer lastMu.Unlock()
	if f.OK {
		lastFix, lastSeen = f, time.Now()
		return f
	}
	if lastFix.OK && time.Since(lastSeen) < 6*time.Second {
		return lastFix
	}
	return f
}

func pollOnce(timeout time.Duration) Fix {
	var fix Fix
	conn, err := net.DialTimeout("tcp", "127.0.0.1:2947", 250*time.Millisecond)
	if err != nil {
		return fix
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	if _, err := conn.Write([]byte("?WATCH={\"enable\":true,\"json\":true}\n")); err != nil {
		return fix
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for sc.Scan() {
		var msg struct {
			Class string  `json:"class"`
			Mode  int     `json:"mode"`
			Lat   float64 `json:"lat"`
			Lon   float64 `json:"lon"`
			USat  int     `json:"uSat"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		switch msg.Class {
		case "TPV":
			fix.Mode = msg.Mode
			fix.Lat = msg.Lat
			fix.Lon = msg.Lon
			fix.OK = msg.Mode >= 2
			if fix.Sats > 0 || fix.Mode < 2 {
				return fix // have both, or no fix worth waiting on
			}
		case "SKY":
			if msg.USat > 0 {
				fix.Sats = msg.USat
			}
			if fix.OK {
				return fix
			}
		}
	}
	return fix
}
