package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// /salud says whether each upstream works, from the last request that
// really went to it. It never makes a request of its own, so checking it
// costs CeX nothing. The answer is 503 while any upstream is paused after
// a block or its last request failed, and 200 otherwise, including before
// the first request.
//
// /salud?destino=algolia (or wss2, or imagenes) checks one upstream only,
// so each one can have its own monitor.

// guardianesSalud lists the upstreams in the order they are reported.
func guardianesSalud() []*guardian { return []*guardian{algolia, wss2, imagenes} }

func paginaSalud(w http.ResponseWriter, r *http.Request) {
	destino := r.URL.Query().Get("destino")
	var b strings.Builder
	sano, encontrado := true, false
	for _, g := range guardianesSalud() {
		if destino != "" && destino != g.nombre {
			continue
		}
		encontrado = true
		linea, ok := g.salud()
		sano = sano && ok
		b.WriteString(linea + "\n")
	}
	if !encontrado {
		http.Error(w, "Destino desconocido: usa algolia, wss2 o imagenes", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if !sano {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_, _ = w.Write([]byte(b.String()))
}

// salud describes the state of one upstream in one line.
func (g *guardian) salud() (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	hora := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	switch {
	case g.ahora().Before(g.pausaHasta):
		return fmt.Sprintf("%s: PAUSADO hasta %s tras un bloqueo (%s)", g.nombre, hora(g.pausaHasta), g.ultimoFallo), false
	case g.ultimaVez.IsZero():
		return fmt.Sprintf("%s: sin peticiones desde el arranque", g.nombre), true
	case g.ultimoFallo != "":
		return fmt.Sprintf("%s: FALLO en la última petición (%s): %s · %s", g.nombre, hora(g.ultimaVez), g.ultimoFallo, g.tiempos.texto()), false
	default:
		return fmt.Sprintf("%s: OK, última petición %s con estado %d · %s", g.nombre, hora(g.ultimaVez), g.ultimoEstado, g.tiempos.texto()), true
	}
}
