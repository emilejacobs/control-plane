// uknomi-edge-ui is the device-local Edge UI Go binary. It serves
// the embedded Next.js bundle (Camera live preview SPA) and the MJPEG
// preview API route on the tailnet + loopback interfaces.
//
// See ADR-029, ADR-030, ADR-032 for the shape decisions. Single-route
// API surface in v1: GET /preview/<camera_id>/stream returns an MJPEG
// multipart response sourced from a local ffmpeg pipe over the
// camera's RTSP URL, looked up by camera_id against the agent-written
// local cameras file.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/emilejacobs/control-plane/internal/agent"
	"github.com/emilejacobs/control-plane/internal/edgeui"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
)

// defaultCamerasPath mirrors the locked decision in the cycle spec —
// the agent's cameras.update handler atomically writes here; Edge UI
// reads from the same path. Both can be overridden via env var
// (CAMERAS_PATH) so dev/test setups can point at a tempdir.
const defaultCamerasPath = "/usr/local/etc/uknomi/cameras.json"

// defaultPort is 5051 per ADR-032 (old Flask Edge UI keeps 5050).
const defaultPort = "5051"

// serverConfig is the bundle of values main passes to startServer.
// startServer is unit-testable; main wires real values + signal
// handling around it.
type serverConfig struct {
	// ListenAddr is "host:port" — startServer creates one listener
	// here. main fans out across multiple listeners (one per
	// detected interface address); tests use one loopback listener.
	ListenAddr  string
	CamerasPath string
}

// startServer builds the mux + handler tree, opens the listener,
// starts http.Serve in a goroutine, and returns the http.Server plus
// the actual address it bound (port 0 → real port). Caller closes
// the server when done.
func startServer(cfg serverConfig) (*http.Server, string, error) {
	mux := buildMux(cfg.CamerasPath)
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return nil, "", fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()
	return srv, ln.Addr().String(), nil
}

// buildMux wires the API routes (PreviewHandler) and the static
// handler (StaticHandler) into one mux. Order matters:
//   - /healthz first (cheap, no auth, used by launchd verify())
//   - /preview/<id>/stream (API) before the static catch-all so the
//     SPA fallback doesn't claim it
//   - "/" static last, catching everything else
func buildMux(camerasPath string) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	// CamerasReader is a closure over the cameras file path — re-read
	// on every preview request so a fresh cameras.update lands without
	// a binary restart.
	reader := edgeui.CamerasReaderFunc(func() (map[string]cameras.Camera, error) {
		return edgeui.ReadCameras(camerasPath)
	})
	preview := edgeui.NewPreviewHandler(reader, edgeui.FFmpegRunner{})

	static := StaticHandler(staticFS)

	// /preview/* — API for /<id>/stream, SPA for everything else.
	mux.HandleFunc("/preview/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stream") {
			preview.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})

	mux.Handle("/", static)
	return mux
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	// launchd's default PATH is minimal; ffmpeg ships under
	// Homebrew's prefix on macOS. The agent ships its own copy of
	// this helper so we reuse it instead of duplicating.
	agent.AugmentSubprocessPath()

	flag.Parse()

	port := os.Getenv("EDGE_UI_PORT")
	if port == "" {
		port = defaultPort
	}
	camerasPath := os.Getenv("CAMERAS_PATH")
	if camerasPath == "" {
		camerasPath = defaultCamerasPath
	}

	addrs, err := edgeui.DetectListenAddrs(edgeui.SystemInterfaces{})
	if err != nil {
		// Fail-open per ADR-032: log and continue with whatever
		// detection returned.
		logger.Warn("interface enumeration failed; binding loopback only", "error", err)
		addrs = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	}
	if len(addrs) == 1 {
		// loopback-only is fine but logable; the tailnet interface is
		// probably not up yet (tailscaled may lag behind launchd).
		logger.Warn("no tailnet interface detected; binding loopback only",
			"hint", "tailscaled may not be up yet")
	}

	mux := buildMux(camerasPath)

	// One http.Server per detected address sharing the same mux.
	// Failing to bind one address doesn't kill the others — we log
	// and continue (fail-open).
	servers := make([]*http.Server, 0, len(addrs))
	var wg sync.WaitGroup
	for _, a := range addrs {
		listenAddr := net.JoinHostPort(a.String(), port)
		ln, err := net.Listen("tcp", listenAddr)
		if err != nil {
			logger.Warn("listen failed", "addr", listenAddr, "error", err)
			continue
		}
		srv := &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		servers = append(servers, srv)
		wg.Add(1)
		go func(s *http.Server, l net.Listener, addr string) {
			defer wg.Done()
			logger.Info("listening", "addr", addr)
			if err := s.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("serve", "addr", addr, "error", err)
			}
		}(srv, ln, listenAddr)
	}
	if len(servers) == 0 {
		logger.Error("no listeners opened; exiting")
		os.Exit(1)
	}
	logger.Info("uknomi-edge-ui started",
		"listeners", len(servers),
		"cameras_path", camerasPath,
		"port", port)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	logger.Info("shutting down", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
	wg.Wait()
}
