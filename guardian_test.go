package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// reloj is a fake clock, so pauses and cache lifetimes can be tested
// without waiting.
type reloj struct {
	mu sync.Mutex
	t  time.Time
}

func (r *reloj) ahora() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.t }
func (r *reloj) avanzar(d time.Duration) {
	r.mu.Lock()
	r.t = r.t.Add(d)
	r.mu.Unlock()
}

func preparar(t *testing.T, tipo string, h http.HandlerFunc) (*guardian, *reloj, *httptest.Server, *atomic.Int32) {
	t.Helper()
	var llamadas atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	rl := &reloj{t: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	g := nuevoGuardian("prueba", tipo, nuevaCache(cacheMaxima))
	g.ahora = rl.ahora
	return g, rl, srv, &llamadas
}

func pedir(g *guardian, srv *httptest.Server, ruta string, ttl time.Duration) (respuesta, error) {
	return g.obtener(context.Background(), ruta, ttl, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+ruta, nil)
	})
}

func jsonOK(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Write([]byte(`{"ok":true}`))
}

func TestCacheAvoidsRepeatedRequests(t *testing.T) {
	g, rl, srv, n := preparar(t, "application/json", jsonOK)
	for i := 0; i < 3; i++ {
		if _, err := pedir(g, srv, "/a", ttlFicha); err != nil {
			t.Fatal(err)
		}
	}
	if n.Load() != 1 {
		t.Fatalf("3 identical requests within the TTL reached upstream %d times, want 1", n.Load())
	}
	rl.avanzar(ttlFicha)
	pedir(g, srv, "/a", ttlFicha)
	if n.Load() != 2 {
		t.Fatalf("after the TTL the request should reach upstream again, got %d calls", n.Load())
	}
}

func TestIdenticalRequestsInFlightAreCollapsed(t *testing.T) {
	g, _, srv, n := preparar(t, "application/json", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		jsonOK(w, r)
	})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); pedir(g, srv, "/a", ttlFicha) }()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatalf("10 simultaneous identical requests reached upstream %d times, want 1", n.Load())
	}
}

func TestConcurrencyIsCapped(t *testing.T) {
	var ahora, maximo atomic.Int32
	g, _, srv, _ := preparar(t, "application/json", func(w http.ResponseWriter, r *http.Request) {
		v := ahora.Add(1)
		for {
			m := maximo.Load()
			if v <= m || maximo.CompareAndSwap(m, v) {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
		ahora.Add(-1)
		jsonOK(w, r)
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); pedir(g, srv, "/"+string(rune('a'+i)), ttlFicha) }(i)
	}
	wg.Wait()
	if maximo.Load() > maxSimultaneas {
		t.Fatalf("%d requests at once, cap is %d", maximo.Load(), maxSimultaneas)
	}
}

func TestRetryAfterPausesTheUpstream(t *testing.T) {
	bloqueado := true
	g, rl, srv, n := preparar(t, "application/json", func(w http.ResponseWriter, r *http.Request) {
		if bloqueado {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		jsonOK(w, r)
	})
	if _, err := pedir(g, srv, "/a", ttlFicha); !errors.Is(err, errBloqueo) {
		t.Fatalf("429 should be reported as a block, got %v", err)
	}
	if _, err := pedir(g, srv, "/b", ttlFicha); !errors.Is(err, errPausado) {
		t.Fatalf("while paused, requests must not leave, got %v", err)
	}
	if n.Load() != 1 {
		t.Fatalf("a request left while paused: %d calls", n.Load())
	}
	bloqueado = false
	rl.avanzar(119 * time.Second)
	if _, err := pedir(g, srv, "/b", ttlFicha); !errors.Is(err, errPausado) {
		t.Fatalf("Retry-After 120 must pause for 120 s, got %v at 119 s", err)
	}
	rl.avanzar(time.Second)
	if _, err := pedir(g, srv, "/b", ttlFicha); err != nil {
		t.Fatalf("after Retry-After the upstream should be used again, got %v", err)
	}
}

func TestBackoffDoublesAndResets(t *testing.T) {
	estado := http.StatusForbidden
	g, rl, srv, _ := preparar(t, "application/json", func(w http.ResponseWriter, r *http.Request) {
		if estado != http.StatusOK {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(estado)
			w.Write([]byte("<html><title>Just a moment...</title>Your IP: 203.0.113.7</html>"))
			return
		}
		jsonOK(w, r)
	})
	esperar := func(d time.Duration) {
		t.Helper()
		rl.avanzar(d - time.Second)
		if _, err := pedir(g, srv, "/x", ttlFicha); !errors.Is(err, errPausado) {
			t.Fatalf("pause shorter than %s: %v", d, err)
		}
		rl.avanzar(time.Second)
	}
	pedir(g, srv, "/a", ttlFicha)
	esperar(time.Minute)
	pedir(g, srv, "/b", ttlFicha)
	esperar(2 * time.Minute)
	pedir(g, srv, "/c", ttlFicha)
	esperar(4 * time.Minute)
	estado = http.StatusOK
	if _, err := pedir(g, srv, "/d", ttlFicha); err != nil {
		t.Fatal(err)
	}
	estado = http.StatusForbidden
	pedir(g, srv, "/e", ttlFicha)
	esperar(time.Minute)
}

func TestChallengePageWith200IsABlock(t *testing.T) {
	g, _, srv, _ := preparar(t, "application/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><title>Just a moment...</title></html>"))
	})
	if _, err := pedir(g, srv, "/a", ttlFicha); !errors.Is(err, errContenido) {
		t.Fatalf("HTML where JSON was expected must be a block, got %v", err)
	}
	if _, err := pedir(g, srv, "/b", ttlFicha); !errors.Is(err, errPausado) {
		t.Fatalf("after a challenge page the upstream must be paused, got %v", err)
	}
}

func TestResponseSizeIsCapped(t *testing.T) {
	g, _, srv, _ := preparar(t, "application/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(strings.Repeat("a", tamanoMaximo+1)))
	})
	if _, err := pedir(g, srv, "/a", ttlFicha); !errors.Is(err, errDemasiado) {
		t.Fatalf("oversized response must be rejected, got %v", err)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	g, _, srv, n := preparar(t, "application/json", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", http.StatusFound)
			return
		}
		jsonOK(w, r)
	})
	if _, err := pedir(g, srv, "/a", ttlFicha); !errors.Is(err, errRedireccion) {
		t.Fatalf("redirect must be an error, got %v", err)
	}
	if n.Load() != 1 {
		t.Fatalf("redirect was followed: %d calls", n.Load())
	}
	if _, pausado := g.pausado(); pausado {
		t.Fatal("a redirect is not a block")
	}
}

func TestNotFoundIsPassedOnAndNotCached(t *testing.T) {
	g, _, srv, n := preparar(t, "application/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
	})
	for i := 0; i < 2; i++ {
		r, err := pedir(g, srv, "/a", ttlFicha)
		if err != nil || r.Estado != http.StatusNotFound {
			t.Fatalf("404 should be handed to the caller, got %d, %v", r.Estado, err)
		}
	}
	if n.Load() != 2 {
		t.Fatalf("only 200 responses are cached, like CeX's service worker: %d calls", n.Load())
	}
	if _, pausado := g.pausado(); pausado {
		t.Fatal("a 404 is not a block")
	}
}

func TestImagesFollowUpstreamMaxAge(t *testing.T) {
	cc := "max-age=10800, public"
	g, rl, srv, n := preparar(t, "image/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", cc)
		w.Write([]byte("jpeg"))
	})
	pedir(g, srv, "/i.jpg", 0)
	rl.avanzar(10799 * time.Second)
	pedir(g, srv, "/i.jpg", 0)
	if n.Load() != 1 {
		t.Fatalf("within max-age the image must come from the cache: %d calls", n.Load())
	}
	rl.avanzar(time.Second)
	pedir(g, srv, "/i.jpg", 0)
	if n.Load() != 2 {
		t.Fatalf("after max-age the image must be fetched again: %d calls", n.Load())
	}
	cc = "no-store"
	pedir(g, srv, "/j.jpg", 0)
	pedir(g, srv, "/j.jpg", 0)
	if n.Load() != 4 {
		t.Fatalf("no-store must not be cached: %d calls", n.Load())
	}
}

func TestNoCookiesAndBrowserUserAgent(t *testing.T) {
	var cookie, agente atomic.Value
	g, _, srv, _ := preparar(t, "application/json", func(w http.ResponseWriter, r *http.Request) {
		cookie.Store(r.Header.Get("Cookie"))
		agente.Store(r.UserAgent())
		http.SetCookie(w, &http.Cookie{Name: "__cf_bm", Value: "x"})
		jsonOK(w, r)
	})
	pedir(g, srv, "/a", ttlFicha)
	pedir(g, srv, "/b", ttlFicha)
	if c := cookie.Load().(string); c != "" {
		t.Fatalf("a cookie set by upstream was sent back: %q", c)
	}
	if a := agente.Load().(string); a != agenteUsuario {
		t.Fatalf("User-Agent %q", a)
	}
}

func TestCacheStaysWithinBudget(t *testing.T) {
	c := nuevaCache(100)
	ahora := time.Now()
	for _, k := range []string{"a", "b", "c"} {
		c.guardar(k, respuesta{Cuerpo: make([]byte, 40)}, ahora.Add(time.Hour))
	}
	if c.tamano > 100 {
		t.Fatalf("cache holds %d B, budget 100", c.tamano)
	}
	if _, ok := c.leer("a", ahora); ok {
		t.Fatal("the oldest entry should have been evicted first")
	}
	if _, ok := c.leer("c", ahora); !ok {
		t.Fatal("the newest entry should still be there")
	}
}
