package main

import (
	"net/http"
	"strings"
	"testing"
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
