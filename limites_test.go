package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func limitadorDePrueba() (*limitador, *reloj) {
	rl := &reloj{t: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	l := nuevoLimitador(rafagaPaginas, ritmoPaginas)
	l.ahora = rl.ahora
	return l, rl
}

func peticionDesde(remota, realIP string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/search/?stext=x", nil)
	r.RemoteAddr = remota
	if realIP != "" {
		r.Header.Set("X-Real-IP", realIP)
	}
	return r
}

func TestBurstThenRefill(t *testing.T) {
	l, rl := limitadorDePrueba()
	for i := 0; i < rafagaPaginas; i++ {
		if ok, _ := l.permitir("a"); !ok {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}
	ok, espera := l.permitir("a")
	if ok {
		t.Fatal("request past the burst was allowed")
	}
	if espera != 2*time.Second {
		t.Fatalf("at 30 per minute the next token comes in 2 s, got %s", espera)
	}
	rl.avanzar(2 * time.Second)
	if ok, _ := l.permitir("a"); !ok {
		t.Fatal("a token should be back after 2 s")
	}
	if ok, _ := l.permitir("b"); !ok {
		t.Fatal("each client has its own bucket")
	}
}

func TestLimitedRequestNeverReachesTheHandler(t *testing.T) {
	l, _ := limitadorDePrueba()
	llamadas := 0
	h := l.envolver(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { llamadas++ }))
	var ultima *httptest.ResponseRecorder
	for i := 0; i < rafagaPaginas+1; i++ {
		ultima = httptest.NewRecorder()
		h.ServeHTTP(ultima, peticionDesde("198.51.100.4:5000", ""))
	}
	if llamadas != rafagaPaginas {
		t.Fatalf("handler ran %d times, want %d", llamadas, rafagaPaginas)
	}
	if ultima.Code != http.StatusTooManyRequests || ultima.Header().Get("Retry-After") != "2" {
		t.Fatalf("got %d with Retry-After %q, want 429 and 2", ultima.Code, ultima.Header().Get("Retry-After"))
	}
	if !strings.Contains(ultima.Body.String(), "Demasiadas peticiones") {
		t.Fatal("the 429 should be the error page")
	}
}

func TestRealIPOnlyTrustedFromTheProxy(t *testing.T) {
	casos := []struct{ remota, real, quiero string }{
		{"172.18.0.5:4000", "203.0.113.9", "203.0.113.9"},
		{"127.0.0.1:4000", "203.0.113.9", "203.0.113.9"},
		{"198.51.100.4:4000", "203.0.113.9", "198.51.100.4"},
		{"172.18.0.5:4000", "basura", "172.18.0.5"},
		{"[2001:db8:1:2:3:4:5:6]:4000", "", "2001:db8:1:2::/64"},
		{"172.18.0.5:4000", "2001:db8:1:2:aaaa::1", "2001:db8:1:2::/64"},
	}
	for _, c := range casos {
		if got := clienteDe(peticionDesde(c.remota, c.real)); got != c.quiero {
			t.Errorf("remote %s, X-Real-IP %q: got %s, want %s", c.remota, c.real, got, c.quiero)
		}
	}
}

func TestIdleBucketsAreForgotten(t *testing.T) {
	l, rl := limitadorDePrueba()
	l.permitir("a")
	rl.avanzar(time.Minute)
	l.permitir("b")
	if _, ok := l.cubos["a"]; ok {
		t.Fatal("a bucket idle for a full refill should be dropped")
	}
}

func TestTooManyClientsFailsClosed(t *testing.T) {
	l, _ := limitadorDePrueba()
	for i := 0; i < maxClientes; i++ {
		l.cubos[string(rune(i))+"x"] = &cubo{fichas: 1, visto: l.ahora()}
	}
	if ok, _ := l.permitir("nuevo"); ok {
		t.Fatal("a new client must be refused when the table is full")
	}
}

func TestEveryResponseIsNoindexAndRobotsBlocksAll(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", robots)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	h := cabeceras(mux)
	for _, ruta := range []string{"/", "/robots.txt", "/nada"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, ruta, nil))
		if w.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
			t.Errorf("%s has no X-Robots-Tag", ruta)
		}
		if ruta == "/robots.txt" && w.Body.String() != "User-agent: *\nDisallow: /\n" {
			t.Errorf("robots.txt is %q", w.Body.String())
		}
	}
}
