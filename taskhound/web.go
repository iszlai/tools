package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

//go:embed ui.html
var uiHTML []byte

// cmdUI serves the kanban board. It binds the loopback interface only: the
// board is a local file and the API can write to it, so it has no business
// being reachable from the network.
func cmdUI(args []string) error {
	fs, file := newFS("ui")
	port := fs.Int("port", 8787, "port to listen on (0 picks a free one)")
	openBrowser := fs.Bool("open", false, "open the board in a browser")
	parse(fs, args)

	s, err := openStore(*file)
	if err != nil {
		return err
	}
	if _, err := s.Read(); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s", ln.Addr().String())
	fmt.Printf("taskhound %s\nserving %s\npress ctrl-c to stop\n", url, s.Path)
	if *openBrowser {
		browse(url)
	}
	srv := &http.Server{
		Handler:           routes(s),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.Serve(ln)
}

func browse(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	_ = cmd.Start()
}

func routes(s *Store) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(uiHTML)
	})

	mux.HandleFunc("GET /api/board", func(w http.ResponseWriter, r *http.Request) {
		b, err := s.Read()
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"prefix":   b.Prefix,
			"statuses": Statuses,
			"file":     s.Path,
			"issues":   views(b, b.Issues),
		})
	})

	mux.HandleFunc("POST /api/issues", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Status      string   `json:"status"`
			BlockedBy   []string `json:"blocked_by"`
			Labels      []string `json:"labels"`
		}
		if err := decode(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if in.Title == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("title is required"))
			return
		}
		if in.Status == "" {
			in.Status = StatusTodo
		}
		if !validStatus(in.Status) {
			httpError(w, http.StatusBadRequest, fmt.Errorf("bad status %q", in.Status))
			return
		}
		var out issueView
		err := s.Update(func(b *Board) error {
			is := b.Add(in.Title, in.Description, in.Status, in.Labels)
			if err := b.SetBlockedBy(is, in.BlockedBy); err != nil {
				return err
			}
			out = view(b, is)
			return nil
		})
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	})

	// PATCH carries only the fields being changed: a nil pointer means "leave
	// this alone", which is what lets the UI send a single edited field.
	mux.HandleFunc("PATCH /api/issues/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title       *string   `json:"title"`
			Description *string   `json:"description"`
			Status      *string   `json:"status"`
			BlockedBy   *[]string `json:"blocked_by"`
			Labels      *[]string `json:"labels"`
		}
		if err := decode(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if in.Status != nil && !validStatus(*in.Status) {
			httpError(w, http.StatusBadRequest, fmt.Errorf("bad status %q", *in.Status))
			return
		}
		var out issueView
		err := s.Update(func(b *Board) error {
			is, err := b.Get(r.PathValue("id"))
			if err != nil {
				return err
			}
			if in.Title != nil {
				if *in.Title == "" {
					return fmt.Errorf("title cannot be empty")
				}
				is.Title = *in.Title
			}
			if in.Description != nil {
				is.Description = *in.Description
			}
			if in.Status != nil {
				is.Status = *in.Status
			}
			if in.Labels != nil {
				is.Labels = *in.Labels
			}
			if in.BlockedBy != nil {
				if err := b.SetBlockedBy(is, *in.BlockedBy); err != nil {
					return err
				}
			}
			is.UpdatedAt = now()
			out = view(b, is)
			return nil
		})
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("POST /api/issues/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Body   string `json:"body"`
			Author string `json:"author"`
		}
		if err := decode(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if in.Body == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("body is required"))
			return
		}
		if in.Author == "" {
			in.Author = currentUser()
		}
		var out issueView
		err := s.Update(func(b *Board) error {
			is, err := b.Get(r.PathValue("id"))
			if err != nil {
				return err
			}
			now := now()
			is.Comments = append(is.Comments, Comment{At: now, Author: in.Author, Body: in.Body})
			is.UpdatedAt = now
			out = view(b, is)
			return nil
		})
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	})

	return mux
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
