package modules

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/files
var filesUI embed.FS

// filesModule serves a phone file browser jailed to a single root directory
// (the human user's home). All paths are contained under root — `..` is
// neutralized and a prefix check is the belt-and-suspenders backstop. Symlinks
// inside the home that point outside are not chased down (low concern: it's the
// user's own home), which is the one caveat to revisit if this ever exposes a
// shared directory.
type filesModule struct {
	root string
}

func NewFilesModule(root string) *filesModule { return &filesModule{root: root} }

// DefaultFilesRoot picks the human user's home when the service runs as root:
// /home/kali, then /home/pi, then the first /home/* dir, then $HOME, then /root.
func DefaultFilesRoot() string {
	for _, u := range []string{"kali", "pi"} {
		if fi, err := os.Stat("/home/" + u); err == nil && fi.IsDir() {
			return "/home/" + u
		}
	}
	if entries, err := os.ReadDir("/home"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				return "/home/" + e.Name()
			}
		}
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/root"
}

// filesManifest is shared by the real module (in the files-agent process) and
// the proxy (in the web worker) so the dashboard tile is identical either way.
func filesManifest() Manifest {
	return Manifest{
		ID: "files", Name: "Files", Icon: "folder",
		Description: "Browse & upload your home",
		Requires:    Requires{}, // always available
		Actions:     []Action{{ID: "browse", Label: "Browse", Type: "command"}},
		Source:      "bundled",
	}
}

func (m *filesModule) Manifest() Manifest { return filesManifest() }

func (m *filesModule) Mount(mux *http.ServeMux, prefix string, auth Middleware) {
	// Detail UI (open, like the core dashboard shell).
	sub, _ := fs.Sub(filesUI, "assets/files")
	mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(sub))))
	// APIs (auth-gated — these read and write real files).
	mux.HandleFunc(prefix+"api/list", auth(m.handleList))
	mux.HandleFunc(prefix+"api/download", auth(m.handleDownload))
	mux.HandleFunc(prefix+"api/upload", auth(m.handleUpload))
}

// safe joins a client-supplied relative path onto root, neutralizing any `..`
// traversal, and verifies the result stays within root.
func (m *filesModule) safe(rel string) (string, bool) {
	p := filepath.Join(m.root, filepath.Clean("/"+rel))
	if p != m.root && !strings.HasPrefix(p, m.root+string(filepath.Separator)) {
		return "", false
	}
	return p, true
}

type fileEntry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

func (m *filesModule) handleList(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	abs, ok := m.safe(rel)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	fis, err := os.ReadDir(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	out := make([]fileEntry, 0, len(fis))
	for _, fi := range fis {
		e := fileEntry{Name: fi.Name(), Dir: fi.IsDir()}
		if info, err := fi.Info(); err == nil {
			e.Size = info.Size()
			e.MTime = info.ModTime().Unix()
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir // directories first
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"root":    filepath.Base(m.root),
		"path":    strings.TrimPrefix(rel, "/"),
		"entries": out,
	})
}

func (m *filesModule) handleDownload(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	abs, ok := m.safe(rel)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		http.Error(w, "not a file", http.StatusNotFound)
		return
	}
	name := filepath.Base(abs)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("dl") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	} else {
		// Inline view (new tab). Sandbox the response so a hostile file — an
		// HTML/SVG with embedded script captured during an engagement — renders
		// without executing JS or gaining the console's origin (unique opaque
		// origin, no scripts/forms/plugins). Images/text/PDF view fine.
		w.Header().Set("Content-Disposition", `inline; filename="`+name+`"`)
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	http.ServeFile(w, r, abs)
}

func (m *filesModule) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	destDir, ok := m.safe(rel)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if fi, err := os.Stat(destDir); err != nil || !fi.IsDir() {
		http.Error(w, "destination is not a directory", http.StatusBadRequest)
		return
	}
	// Stream parts straight to disk — never buffer the whole file in RAM.
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected multipart", http.StatusBadRequest)
		return
	}
	saved := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if part.FormName() != "file" {
			continue
		}
		name := filepath.Base(part.FileName())
		if name == "" || name == "." || name == string(filepath.Separator) {
			continue
		}
		dest := filepath.Join(destDir, name)
		f, err := os.Create(dest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, err = io.Copy(f, part)
		f.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		saved++
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"saved": saved})
}
