package man

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

//go:embed page.go.html
var pageTmpl string

type Options struct {
	Addr   string // default 127.0.0.1:0
	NoOpen bool
}

func Serve(opts Options) error {
	entries, err := loadEntries()
	if err != nil {
		return err
	}
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}

	tmpl := template.Must(template.New("page").Parse(pageTmpl))
	byKey := map[string]Entry{}
	for _, e := range entries {
		byKey[e.Standard+"/"+e.Slug] = e
	}

	mux := http.NewServeMux()
	render := func(w http.ResponseWriter, cur *Entry) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, map[string]any{"Entries": entries, "Cur": cur})
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { render(w, nil) })
	mux.HandleFunc("GET /p/{std}/{slug}", func(w http.ResponseWriter, r *http.Request) {
		e, ok := byKey[r.PathValue("std")+"/"+r.PathValue("slug")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		render(w, &e)
	})
	mux.HandleFunc("GET /index.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(entries) // for client side search
	})

	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	fmt.Println("redline man serving at", url, "(Ctrl-c to stop)")
	if !opts.NoOpen {
		go openBrowser(url)
	}

	srv := &http.Server{Handler: mux}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.Serve(ln)
	<-ctx.Done()

	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}

func openBrowser(url string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	if err := exec.Command(opener, url).Start(); err != nil {
		fmt.Fprintf(os.Stderr, "couldn't open a browser (%v), visit %s manually\n", err, url)
	}
}
