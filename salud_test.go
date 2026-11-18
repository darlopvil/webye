package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHealthBeforeAnyRequestIsGreen(t *testing.T) {
	algoliaDePrueba(t, jsonDe(respuestaPaginada(1, 1, 0)))
	wss2DePrueba(t, jsonDe(fichaIphone))
	imagenesDePrueba(t, jpeg)
	w := pedirRuta(t, "/salud")
	if w.Code != http.StatusOK || strings.Count(w.Body.String(), "sin peticiones desde el arranque") != 3 {
		t.Fatalf("got %d:\n%s", w.Code, w.Body.String())
	}
}

func TestHealthReportsSuccessBlocksAndChangesWithoutCallingCeX(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	cuerpo := respuestaPaginada(1, 1, 0)
	n := algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) { jsonDe(cuerpo)(w, r) })
	wss2DePrueba(t, jsonDe(fichaIphone))
	imagenesDePrueba(t, jpeg)

	pedirRuta(t, "/search/?stext=a")
	w := pedirRuta(t, "/salud?destino=algolia")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "algolia: OK") {
		t.Fatalf("after a good search: %d %s", w.Code, w.Body.String())
	}

	// The API changes shape: the answer arrives but cannot be read.
	cuerpo = `{"resultados":"otra cosa"}`
	pedirRuta(t, "/search/?stext=b")
	w = pedirRuta(t, "/salud?destino=algolia")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "FALLO") {
		t.Fatalf("after a changed answer: %d %s", w.Code, w.Body.String())
	}
	// The other upstreams are unaffected, so their own monitors stay green.
	if w := pedirRuta(t, "/salud?destino=wss2"); w.Code != http.StatusOK {
		t.Fatalf("wss2 should stay green: %d", w.Code)
	}
	if w := pedirRuta(t, "/salud"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("the global check should be red: %d", w.Code)
	}

	llamadas := n.Load()
	for i := 0; i < 5; i++ {
		pedirRuta(t, "/salud")
	}
	if n.Load() != llamadas {
		t.Fatal("/salud made requests to CeX")
	}
}

func TestHealthShowsAPause(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	pedirRuta(t, "/search/?stext=a")
	w := pedirRuta(t, "/salud?destino=algolia")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "PAUSADO hasta") || !strings.Contains(w.Body.String(), "429") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestHealthUnknownUpstream(t *testing.T) {
	if w := pedirRuta(t, "/salud?destino=otro"); w.Code != http.StatusNotFound {
		t.Fatalf("got %d", w.Code)
	}
}

// agotado is what the HTTP client returns when tiempoMaximo runs out.
type agotado struct{}

func (agotado) Error() string   { return "Client.Timeout exceeded" }
func (agotado) Timeout() bool   { return true }
func (agotado) Temporary() bool { return true }

func TestHealthCountsHowLongAnswersTake(t *testing.T) {
	g := nuevoGuardian("prueba", "application/json", nuevaCache(cacheMaxima))
	for _, d := range []time.Duration{100 * time.Millisecond, 1200 * time.Millisecond, 3500 * time.Millisecond, 12 * time.Second} {
		g.anotar(http.StatusOK, nil, d)
	}
	g.anotar(0, fmt.Errorf("Get wss2.cex.es.webuy.io: %w", agotado{}), 25*time.Second)
	linea, _ := g.salud()
	quiero := "5 peticiones; tardaron 1 s o más: 4, 3 s o más: 3, 10 s o más: 2; agotadas: 1; la más lenta: 25,0 s"
	if !strings.Contains(linea, quiero) {
		t.Fatalf("got %q", linea)
	}
}
