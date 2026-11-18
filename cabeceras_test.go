package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEveryRouteCarriesThePrivacyHeadersAndNoCookies(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, jsonDe(respuestaPaginada(1, 1, 0)))
	wss2DePrueba(t, jsonDe(fichaIphone))
	imagenesDePrueba(t, jpeg)
	quiero := map[string]string{
		"Cache-Control":           "no-store",
		"Content-Security-Policy": politicaContenido,
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"X-Robots-Tag":            "noindex, nofollow",
	}
	h := rutas()
	for _, ruta := range []string{
		"/", "/about", "/robots.txt", "/nada", "/favicon.ico",
		"/static/estilo.css", "/static/fuentes/anton.woff2", "/static/sin-foto.svg",
		"/search/?stext=x", "/search/?stext=", "/product-detail/?id=SAPPIP17P256GNCLIBA",
		"/product-detail/?id=..", "/img/a/x.jpg", "/img/a.txt",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, ruta, nil))
		for k, v := range quiero {
			// Static files and photos have their own caching, see
			// estaticos_test.go; every other header applies to them too.
			if k == "Cache-Control" && (strings.HasPrefix(ruta, "/static/") || strings.HasPrefix(ruta, "/img/")) {
				continue
			}
			if got := w.Header().Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", ruta, k, got, v)
			}
		}
		if c := w.Header().Values("Set-Cookie"); len(c) > 0 {
			t.Errorf("%s sets cookies: %v", ruta, c)
		}
	}
}

func TestPagesNeedNothingTheCSPForbids(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, jsonDe(respuestaPaginada(1, 1, 0)))
	wss2DePrueba(t, jsonDe(fichaIphone))
	for _, ruta := range []string{"/", "/about", "/search/?stext=x", "/product-detail/?id=SAPPIP17P256GNCLIBA", "/nada"} {
		html := strings.ToLower(pedirRuta(t, ruta).Body.String())
		for _, prohibido := range []string{"<script", " style=", "<style", `="javascript:`, `="http://`, "<iframe"} {
			if strings.Contains(html, prohibido) {
				t.Errorf("%s contains %q, which the CSP would block or which reaches outside", ruta, prohibido)
			}
		}
		// The only absolute links allowed point to the source code, CeX's
		// own site in the about page and the license.
		for _, trozo := range strings.Split(html, `href="https://`)[1:] {
			destino := trozo[:strings.IndexByte(trozo, '"')]
			if !strings.HasPrefix(destino, "github.com/darlopvil/webye") && !strings.HasPrefix(destino, "es.webuy.com/") {
				t.Errorf("%s links to https://%s", ruta, destino)
			}
		}
	}
}
