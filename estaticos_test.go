package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func pedirConCabecera(t *testing.T, ruta, cabecera, valor string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, ruta, nil)
	r.Header.Set(cabecera, valor)
	rutas().ServeHTTP(w, r)
	return w
}

func TestPagesLinkVersionedStaticFiles(t *testing.T) {
	html := pedirRuta(t, "/").Body.String()
	for _, nombre := range []string{"estilo.css", "favicon.svg"} {
		if v := estaticos["/static/"+nombre].version; v == "" || !strings.Contains(html, `href="/static/`+nombre+`?v=`+v+`"`) {
			t.Errorf("%s not linked with its version", nombre)
		}
	}
}

func TestVersionedStaticFilesAreKeptForAYear(t *testing.T) {
	for _, ruta := range []string{"/static/estilo.css", "/static/fuentes/anton.woff2", "/static/favicon.svg"} {
		w := pedirRuta(t, estatico(strings.TrimPrefix(ruta, "/static/")))
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("%s: %d, Cache-Control %q", ruta, w.Code, w.Header().Get("Cache-Control"))
		}
	}
	if tipo := pedirRuta(t, estatico("fuentes/anton.woff2")).Header().Get("Content-Type"); tipo != "font/woff2" {
		t.Errorf("font served as %q", tipo)
	}
}

func TestTheStylesheetLoadsVersionedFonts(t *testing.T) {
	css := pedirRuta(t, estatico("estilo.css")).Body.String()
	for _, fuente := range []string{"fuentes/anton.woff2", "fuentes/permanent-marker.woff2"} {
		if !strings.Contains(css, `url("`+estatico(fuente)+`")`) {
			t.Errorf("stylesheet does not load %s with its version", fuente)
		}
	}
}

func TestStaticFilesWithoutTheirVersionMustBeChecked(t *testing.T) {
	for _, ruta := range []string{"/static/estilo.css", "/static/estilo.css?v=viejo", "/static/fuentes/OFL-Anton.txt"} {
		w := pedirRuta(t, ruta)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("ETag") == "" {
			t.Fatalf("%s: %d, Cache-Control %q, ETag %q", ruta, w.Code, w.Header().Get("Cache-Control"), w.Header().Get("ETag"))
		}
	}
	etag := pedirRuta(t, "/static/estilo.css").Header().Get("ETag")
	if w := pedirConCabecera(t, "/static/estilo.css", "If-None-Match", etag); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("an unchanged file should be a 304 without body, got %d with %d B", w.Code, w.Body.Len())
	}
}

func TestStaticFoldersAreNotListed(t *testing.T) {
	for _, ruta := range []string{"/static/", "/static/fuentes/", "/static/nada.css"} {
		if w := pedirRuta(t, ruta); w.Code != http.StatusNotFound {
			t.Errorf("%s: %d", ruta, w.Code)
		}
	}
}

func TestPagesAreStillNeverKept(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	algoliaDePrueba(t, jsonDe(respuestaPaginada(1, 1, 0)))
	wss2DePrueba(t, jsonDe(fichaIphone))
	for _, ruta := range []string{"/", "/about", "/search/?stext=x", "/product-detail/?id=SAPPIP17P256GNCLIBA"} {
		if c := pedirRuta(t, ruta).Header().Get("Cache-Control"); c != "no-store" {
			t.Errorf("%s: Cache-Control %q", ruta, c)
		}
	}
}

func TestPhotosAreKeptByTheBrowserAsLongAsByWebye(t *testing.T) {
	imagenesDePrueba(t, jpeg) // CeX says max-age=10800
	rl := &reloj{t: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	imagenes.ahora = rl.ahora
	if c := pedirRuta(t, "/img/a/x.jpg").Header().Get("Cache-Control"); c != "private, max-age=10800" {
		t.Fatalf("fresh photo: %q", c)
	}
	// An hour later it comes from webye's cache, with two hours left.
	rl.avanzar(time.Hour)
	if c := pedirRuta(t, "/img/a/x.jpg").Header().Get("Cache-Control"); c != "private, max-age=7200" {
		t.Fatalf("cached photo: %q", c)
	}
}

func TestThePlaceholderIsKeptAsLongAsCeXAllows(t *testing.T) {
	imagenesDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=300, public")
		w.Header().Set("Location", "https://es.static.webuy.com/images/default/defimg1_m.jpg")
		w.WriteHeader(http.StatusFound)
	})
	imagenes.ahora = (&reloj{t: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}).ahora
	if c := pedirRuta(t, "/img/a/x.jpg").Header().Get("Cache-Control"); c != "private, max-age=300" {
		t.Fatalf("placeholder: %q", c)
	}
}

func TestPhotosCeXDoesNotLetBeKeptAreNotKept(t *testing.T) {
	imagenesDePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte("\xff\xd8jpeg"))
	})
	if c := pedirRuta(t, "/img/a/x.jpg").Header().Get("Cache-Control"); c != "no-store" {
		t.Fatalf("got %q", c)
	}
}
